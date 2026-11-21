package browsercookie

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/installdir"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

const HeaderTTL = 5 * time.Minute

const cookieHeaderFailureTTL = time.Minute

type cookieHeaderCacheEntry struct {
	header    string
	err       error
	readAt    time.Time
	expiresAt time.Time
}

// Reads for one source are serialized; a slow browser never delays another.
var cookieHeaderCache = struct {
	sync.Mutex
	entries map[string]cookieHeaderCacheEntry
	reads   map[string]*sync.Mutex
}{entries: map[string]cookieHeaderCacheEntry{}, reads: map[string]*sync.Mutex{}}

func cachedCookieHeader(key string) (cookieHeaderCacheEntry, bool) {
	cookieHeaderCache.Lock()
	defer cookieHeaderCache.Unlock()
	entry, ok := cookieHeaderCache.entries[key]
	return entry, ok && time.Now().Before(entry.expiresAt)
}

func storeCookieHeader(key string, entry cookieHeaderCacheEntry) {
	entry.readAt = time.Now()
	cookieHeaderCache.Lock()
	cookieHeaderCache.entries[key] = entry
	cookieHeaderCache.Unlock()
}

func cookieReadLock(key string) *sync.Mutex {
	cookieHeaderCache.Lock()
	defer cookieHeaderCache.Unlock()
	lock := cookieHeaderCache.reads[key]
	if lock == nil {
		lock = &sync.Mutex{}
		cookieHeaderCache.reads[key] = lock
	}
	return lock
}

// Source is everything one request needs to read its cookies: the
// bridge script, the site, and the browser profile resolved for the request.
type Source struct {
	Bridge string
	URL    string
	Signin signin.Source
}

func (s Source) CacheKey() string {
	return strings.Join([]string{s.Bridge, s.URL, s.Signin.ProfileID, string(s.Signin.Browser), s.Signin.Dir}, "\x00")
}

func (s Source) bridgeArgs() []string {
	args := []string{s.Bridge, "--browser", string(s.Signin.Browser)}
	if s.Signin.Dir != "" {
		args = append(args, "--profile-dir", s.Signin.Dir)
	}
	if s.URL != "" {
		args = append(args, s.URL)
	}
	return args
}

func LoadHeader(parent context.Context, source Source) (header string, err error) {
	origin := Origin{read: true, readAt: time.Now()}
	defer func() {
		origin.failed = err != nil
		noteCookieOrigin(parent, origin)
	}()
	if source.Bridge == "" {
		return "", fmt.Errorf("cookie bridge not found (set AGENTHAIL_COOKIE_BRIDGE or install cookie.mjs alongside agenthail)")
	}
	if source.Signin.Browser == "" {
		return "", fmt.Errorf("cookie bridge: no browser profile resolved")
	}
	key := source.CacheKey()
	if entry, ok := cachedCookieHeader(key); ok {
		origin.cached, origin.readAt = true, entry.readAt
		return entry.header, entry.err
	}
	lock := cookieReadLock(key)
	lock.Lock()
	defer lock.Unlock()
	if entry, ok := cachedCookieHeader(key); ok {
		origin.cached, origin.readAt = true, entry.readAt
		return entry.header, entry.err
	}
	node, err := surface.ShippedNode()
	if err != nil {
		return "", fmt.Errorf("cookie bridge: %w", err)
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := surface.ProcessGroupCommand(ctx, node, source.bridgeArgs()...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("cookie bridge (%s): %w", source.Signin.Browser.Label(), ctx.Err())
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			err = fmt.Errorf("cookie bridge (%s): %w: %s", source.Signin.Browser.Label(), err, surface.DiagnosticExcerpt(detail))
		} else {
			err = fmt.Errorf("cookie bridge (%s): %w", source.Signin.Browser.Label(), err)
		}
		storeCookieHeader(key, cookieHeaderCacheEntry{err: err, expiresAt: time.Now().Add(cookieHeaderFailureTTL)})
		return "", err
	}
	header = strings.TrimSpace(string(output))
	if header == "" {
		err := fmt.Errorf("cookie bridge returned no cookies")
		storeCookieHeader(key, cookieHeaderCacheEntry{err: err, expiresAt: time.Now().Add(cookieHeaderFailureTTL)})
		return "", err
	}
	storeCookieHeader(key, cookieHeaderCacheEntry{header: header, expiresAt: time.Now().Add(HeaderTTL)})
	return header, nil
}

// Origin records, for a request whose context carries it, where
// LoadHeader took that request's cookies from.
type Origin struct {
	read   bool
	cached bool
	failed bool
	readAt time.Time
}

type originKey struct{}

func WithOrigin(ctx context.Context, origin *Origin) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

func noteCookieOrigin(ctx context.Context, origin Origin) {
	if recorded, ok := ctx.Value(originKey{}).(*Origin); ok {
		*recorded = origin
	}
}

func (o Origin) String() string {
	switch {
	case !o.read:
		return "not read"
	case o.cached && o.failed:
		return fmt.Sprintf("not read: the cached result is a failed read from %s ago", time.Since(o.readAt).Round(time.Second))
	case o.failed:
		return "not read: reading them from the browser failed"
	case o.cached:
		return fmt.Sprintf("cached, read %s ago", time.Since(o.readAt).Round(time.Second))
	}
	return "read fresh from the browser"
}

func Invalidate(source Source) {
	if source.Bridge == "" {
		return
	}
	cookieHeaderCache.Lock()
	delete(cookieHeaderCache.entries, source.CacheKey())
	cookieHeaderCache.Unlock()
}

// ConfiguredBridge locates cookie.mjs: AGENTHAIL_COOKIE_BRIDGE, then a
// copy in the install directory or on PATH.
func ConfiguredBridge() string {
	if bridge := os.Getenv("AGENTHAIL_COOKIE_BRIDGE"); bridge != "" {
		return bridge
	}
	return cookieBridgePath("cookie")
}

func cookieBridgePath(name string) string {
	for _, check := range []string{name, name + ".mjs"} {
		if exe, err := os.Executable(); err == nil {
			sibling := filepath.Join(installdir.Of(exe), check)
			if _, err := os.Stat(sibling); err == nil {
				return sibling
			}
		}
		if p, err := exec.LookPath(check); err == nil {
			return p
		}
	}
	return ""
}
