package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/installdir"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

const maxDiagnosticBytes = 2048

const cookieHeaderTTL = 5 * time.Minute
const cookieHeaderFailureTTL = time.Minute

func diagnosticExcerpt(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxDiagnosticBytes {
		return value
	}
	cut := value[:maxDiagnosticBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s... [truncated; %d bytes total]", strings.TrimSpace(cut), len(value))
}

type cookieHeaderCacheEntry struct {
	header    string
	err       error
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

// CookieSource is everything one request needs to read its cookies: the
// bridge script, the site, and the browser profile resolved for the request.
type CookieSource struct {
	Bridge string
	URL    string
	Signin signin.Source
}

func (s CookieSource) cacheKey() string {
	return strings.Join([]string{s.Bridge, s.URL, s.Signin.ProfileID, string(s.Signin.Browser), s.Signin.Dir}, "\x00")
}

func (s CookieSource) bridgeArgs() []string {
	args := []string{s.Bridge, "--browser", string(s.Signin.Browser)}
	if s.Signin.Dir != "" {
		args = append(args, "--profile-dir", s.Signin.Dir)
	}
	if s.URL != "" {
		args = append(args, s.URL)
	}
	return args
}

func sidecarPath() (string, error) {
	if p := os.Getenv("AGENTHAIL_SIDECAR"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"sidecar.py", "claude-worker"} {
			sibling := filepath.Join(installdir.Of(exe), name)
			if _, err := os.Stat(sibling); err == nil {
				return sibling, nil
			}
		}
	}
	for _, name := range []string{"sidecar.py", "claude-worker"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("sidecar not found (set AGENTHAIL_SIDECAR, install it alongside agenthail, or put it on PATH)")
}

func sidecarPython() (string, error) {
	candidates := []string{}
	if configured := os.Getenv("AGENTHAIL_PYTHON"); configured != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates, "python3.14", "python3.13", "python3.12", "python3.11", "python3.10", "python3")
	}
	var failures []string
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			failures = append(failures, candidate+": not found")
			continue
		}
		if err := exec.Command(path, "-c", "import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)").Run(); err != nil {
			version, _ := exec.Command(path, "--version").CombinedOutput()
			failures = append(failures, fmt.Sprintf("%s: %s", path, strings.TrimSpace(string(version))))
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("Python 3.10+ is required for the sidecar (set AGENTHAIL_PYTHON; checked %s)", strings.Join(failures, "; "))
}

type workerResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

// startFailure names a working folder that is gone. A command in its own
// process group skips os.StartProcess's check of its folder, so the child's
// failed chdir reports the program as missing instead.
func startFailure(cmd *exec.Cmd, err error) error {
	var path *fs.PathError
	if cmd.Dir == "" || !errors.As(err, &path) {
		return err
	}
	if info, statErr := os.Stat(cmd.Dir); statErr == nil && info.IsDir() {
		return err
	}
	return surface.DeliveryUnavailable(fmt.Errorf("the folder %s is gone", cmd.Dir))
}

func processGroupCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			return err
		}
		return nil
	}
	return cmd
}

func loadCookieHeader(parent context.Context, source CookieSource) (string, error) {
	if source.Bridge == "" {
		return "", fmt.Errorf("cookie bridge not found (set AGENTHAIL_COOKIE_BRIDGE or install cookie.mjs alongside agenthail)")
	}
	if source.Signin.Browser == "" {
		return "", fmt.Errorf("cookie bridge: no browser profile resolved")
	}
	key := source.cacheKey()
	if entry, ok := cachedCookieHeader(key); ok {
		return entry.header, entry.err
	}
	lock := cookieReadLock(key)
	lock.Lock()
	defer lock.Unlock()
	if entry, ok := cachedCookieHeader(key); ok {
		return entry.header, entry.err
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	cmd := processGroupCommand(ctx, "node", source.bridgeArgs()...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("cookie bridge (%s): %w", source.Signin.Browser.Label(), ctx.Err())
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			err = fmt.Errorf("cookie bridge (%s): %w: %s", source.Signin.Browser.Label(), err, diagnosticExcerpt(detail))
		} else {
			err = fmt.Errorf("cookie bridge (%s): %w", source.Signin.Browser.Label(), err)
		}
		storeCookieHeader(key, cookieHeaderCacheEntry{err: err, expiresAt: time.Now().Add(cookieHeaderFailureTTL)})
		return "", err
	}
	header := strings.TrimSpace(string(output))
	if header == "" {
		err := fmt.Errorf("cookie bridge returned no cookies")
		storeCookieHeader(key, cookieHeaderCacheEntry{err: err, expiresAt: time.Now().Add(cookieHeaderFailureTTL)})
		return "", err
	}
	storeCookieHeader(key, cookieHeaderCacheEntry{header: header, expiresAt: time.Now().Add(cookieHeaderTTL)})
	return header, nil
}

func invalidateCookieHeader(source CookieSource) {
	if source.Bridge == "" {
		return
	}
	cookieHeaderCache.Lock()
	delete(cookieHeaderCache.entries, source.cacheKey())
	cookieHeaderCache.Unlock()
}

func sidecarRequestWithCookies(parent context.Context, method, url string, headers map[string]string, body string, cookies CookieSource, timeout time.Duration) (int, string, error) {
	worker, err := sidecarPath()
	if err != nil {
		return 0, "", surface.DeliveryUnavailable(err)
	}
	python, err := sidecarPython()
	if err != nil {
		return 0, "", surface.DeliveryUnavailable(err)
	}
	cookie, err := loadCookieHeader(parent, cookies)
	if err != nil {
		return 0, "", surface.DeliveryUnavailable(err)
	}
	requestHeaders := make(map[string]string, len(headers)+1)
	for key, value := range headers {
		requestHeaders[key] = value
	}
	if cookie != "" {
		requestHeaders["cookie"] = cookie
	}
	req := map[string]any{
		"method":  method,
		"url":     url,
		"headers": requestHeaders,
		"timeout": int(timeout.Seconds()),
	}
	if body != "" {
		req["body"] = body
	}
	input, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(parent, timeout+10*time.Second)
	defer cancel()
	cmd := processGroupCommand(ctx, python, worker)
	cmd.Stdin = strings.NewReader(string(input))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return 0, "", ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = detail[:500] + "..."
		}
		if detail != "" {
			return 0, "", fmt.Errorf("sidecar failed: %w (%s)", err, detail)
		}
		return 0, "", fmt.Errorf("sidecar failed: %w", err)
	}
	var resp workerResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		raw := stdout.String()
		if len(raw) > 500 {
			raw = raw[:500] + "..."
		}
		return 0, "", fmt.Errorf("parse sidecar output: %w (raw: %s)", err, raw)
	}
	if resp.Error != "" {
		return 0, "", fmt.Errorf("sidecar: %s", resp.Error)
	}
	if resp.Status == 401 || resp.Status == 403 {
		invalidateCookieHeader(cookies)
	}
	return resp.Status, resp.Body, nil
}

// configuredCookieBridge locates cookie.mjs: AGENTHAIL_COOKIE_BRIDGE, then a
// copy in the install directory or on PATH.
func configuredCookieBridge() string {
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
