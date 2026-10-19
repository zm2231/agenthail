package signin

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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

// Detect lists the installed browsers and their profiles. It reads profile
// metadata only, never a cookie store.
func Detect(ctx context.Context, home string) []DetectedBrowser {
	results := make([]*DetectedBrowser, len(Browsers))
	done := make(chan struct{}, len(Browsers))
	for i, browser := range Browsers {
		go func() {
			defer func() { done <- struct{}{} }()
			probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
			defer cancel()
			found := make(chan *DetectedBrowser, 1)
			go func() { found <- probe(home, browser) }()
			select {
			case result := <-found:
				results[i] = result
			case <-probeCtx.Done():
				results[i] = &DetectedBrowser{Browser: browser, Label: browser.Label(), Detail: "reading this browser's profiles did not finish in time", Profiles: []DetectedProfile{}}
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
		var ok bool
		if profiles, ok = firefoxProfiles(root); !ok {
			return nil
		}
	default:
		root := chromiumRoot(home, browser)
		if root == "" {
			return nil
		}
		var ok bool
		if profiles, ok = chromiumProfiles(root); !ok {
			return nil
		}
	}
	result := &DetectedBrowser{Browser: browser, Label: browser.Label(), Available: true, Profiles: profiles}
	if len(profiles) == 0 {
		result.Available = false
		result.Detail = "no profiles with a cookie store"
	}
	return result
}

func chromiumProfiles(root string) ([]DetectedProfile, bool) {
	data, err := os.ReadFile(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, false
	}
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return []DetectedProfile{}, true
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
	return profiles, true
}

func chromiumCookieStoreExists(dir string) bool {
	for _, path := range []string{filepath.Join(dir, "Network", "Cookies"), filepath.Join(dir, "Cookies")} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func firefoxProfiles(root string) ([]DetectedProfile, bool) {
	file, err := os.Open(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return nil, false
	}
	defer file.Close()
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
	scanner := bufio.NewScanner(file)
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
	return profiles, true
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
