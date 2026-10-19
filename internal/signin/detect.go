package signin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type DetectedProfile struct {
	Profile string `json:"profile"`
	Name    string `json:"name"`
}

type DetectedBrowser struct {
	Browser   Browser           `json:"browser"`
	Label     string            `json:"label"`
	Available bool              `json:"available"`
	Detail    string            `json:"detail,omitempty"`
	Profiles  []DetectedProfile `json:"profiles"`
}

// ProbeTimeout bounds each browser's probe. A probe that blocks, for example
// on a protected path, reports the browser unavailable instead of holding up
// the list.
var ProbeTimeout = 2 * time.Second

var safariApp = "/Applications/Safari.app"

var probeBrowser = probe

const maxMetadataBytes = 16 << 20

// A filesystem call cannot be cancelled, so a probe that outlives its timeout
// keeps running. At most one probe per home and browser runs at a time; later
// lists report the browser unavailable until it returns.
var probes = struct {
	sync.Mutex
	running map[string]bool
}{running: map[string]bool{}}

func startProbe(key string) bool {
	probes.Lock()
	defer probes.Unlock()
	if probes.running[key] {
		return false
	}
	probes.running[key] = true
	return true
}

func finishProbe(key string) {
	probes.Lock()
	delete(probes.running, key)
	probes.Unlock()
}

func unavailable(browser Browser, detail string) *DetectedBrowser {
	return &DetectedBrowser{Browser: browser, Label: browser.Label(), Detail: detail, Profiles: []DetectedProfile{}}
}

// Detect lists the installed browsers and their profiles. It reads profile
// metadata only, never a cookie store.
func Detect(ctx context.Context, home string) []DetectedBrowser {
	results := make([]*DetectedBrowser, len(Browsers))
	done := make(chan struct{}, len(Browsers))
	for i, browser := range Browsers {
		go func() {
			defer func() { done <- struct{}{} }()
			key := home + "\x00" + string(browser)
			if !startProbe(key) {
				results[i] = unavailable(browser, "an earlier read of this browser's profiles has not finished")
				return
			}
			probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
			defer cancel()
			found := make(chan *DetectedBrowser, 1)
			go func() {
				defer finishProbe(key)
				found <- probeBrowser(home, browser)
			}()
			select {
			case result := <-found:
				results[i] = result
			case <-probeCtx.Done():
				results[i] = unavailable(browser, "reading this browser's profiles did not finish in time")
			}
		}()
	}
	for range Browsers {
		<-done
	}
	detected := []DetectedBrowser{}
	for _, result := range results {
		if result != nil {
			detected = append(detected, *result)
		}
	}
	return detected
}

func probe(home string, browser Browser) *DetectedBrowser {
	var profiles []DetectedProfile
	switch {
	case browser == Safari:
		if runtime.GOOS != "darwin" {
			return nil
		}
		if _, err := os.Stat(safariApp); err != nil {
			return nil
		}
		profiles = []DetectedProfile{{Profile: SafariProfile, Name: "Safari"}}
	case browser == Firefox:
		root := firefoxRoot(home)
		if root == "" {
			return nil
		}
		var err error
		if profiles, err = firefoxProfiles(root); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return unavailable(browser, err.Error())
		}
	default:
		root := chromiumRoot(home, browser)
		if root == "" {
			return nil
		}
		var err error
		if profiles, err = chromiumProfiles(root); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return unavailable(browser, err.Error())
		}
	}
	result := &DetectedBrowser{Browser: browser, Label: browser.Label(), Available: true, Profiles: profiles}
	if len(profiles) == 0 {
		result.Available = false
		result.Detail = "no profiles with a cookie store"
	}
	return result
}

// readMetadata reads a small regular file without blocking on special files:
// the open is non-blocking and anything but a regular file is refused.
func readMetadata(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetadataBytes {
		return nil, fmt.Errorf("%s is too large", filepath.Base(path))
	}
	return data, nil
}

func chromiumProfiles(root string) ([]DetectedProfile, error) {
	data, err := readMetadata(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, err
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return []DetectedProfile{}, nil
	}
	profiles := []DetectedProfile{}
	for dir, info := range state.Profile.InfoCache {
		if !validProfileDir(dir) || !chromiumCookieStoreExists(filepath.Join(root, dir)) {
			continue
		}
		name := info.Name
		if name == "" {
			name = dir
		}
		profiles = append(profiles, DetectedProfile{Profile: dir, Name: name})
	}
	sortProfiles(profiles)
	return profiles, nil
}

func chromiumCookieStoreExists(dir string) bool {
	for _, path := range []string{filepath.Join(dir, "Network", "Cookies"), filepath.Join(dir, "Cookies")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func firefoxProfiles(root string) ([]DetectedProfile, error) {
	data, err := readMetadata(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return nil, err
	}
	profiles := []DetectedProfile{}
	var name, path string
	inProfile := false
	flush := func() {
		if inProfile && path != "" {
			dir := path
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(root, filepath.FromSlash(path))
			}
			if info, err := os.Stat(filepath.Join(dir, "cookies.sqlite")); err == nil && !info.IsDir() {
				if name == "" {
					name = path
				}
				profiles = append(profiles, DetectedProfile{Profile: path, Name: name})
			}
		}
		name, path = "", ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			inProfile = strings.HasPrefix(line, "[Profile")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !inProfile {
			continue
		}
		switch key {
		case "Name":
			name = value
		case "Path":
			path = value
		}
	}
	flush()
	sortProfiles(profiles)
	return profiles, nil
}

func sortProfiles(profiles []DetectedProfile) {
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Name != profiles[j].Name {
			return profiles[i].Name < profiles[j].Name
		}
		return profiles[i].Profile < profiles[j].Profile
	})
}

func Find(detected []DetectedBrowser, browser Browser, profile string) (DetectedBrowser, DetectedProfile, bool) {
	for _, candidate := range detected {
		if candidate.Browser != browser {
			continue
		}
		for _, p := range candidate.Profiles {
			if p.Profile == profile {
				return candidate, p, true
			}
		}
		return candidate, DetectedProfile{}, false
	}
	return DetectedBrowser{}, DetectedProfile{}, false
}
