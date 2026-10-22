package signin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"howett.net/plist"
)

var macBrowsers = map[string]Browser{
	"com.google.chrome":          Chrome,
	"com.brave.browser":          Brave,
	"company.thebrowser.browser": Arc,
	"org.chromium.chromium":      Chromium,
	"com.microsoft.edgemac":      Edge,
	"org.mozilla.firefox":        Firefox,
	"com.apple.safari":           Safari,
}

var linuxBrowsers = map[string]Browser{
	"google-chrome":         Chrome,
	"google-chrome-stable":  Chrome,
	"brave-browser":         Brave,
	"chromium":              Chromium,
	"chromium-browser":      Chromium,
	"microsoft-edge":        Edge,
	"microsoft-edge-stable": Edge,
	"firefox":               Firefox,
	"firefox-esr":           Firefox,
}

var xdgDefaultBrowser = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "xdg-settings", "get", "default-web-browser").Output()
	return strings.TrimSpace(string(output)), err
}

func launchServicesPlist(home string) string {
	return filepath.Join(home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist")
}

// ErrNoDefaultBrowser is a computer with no default browser, such as a Linux
// server without a desktop, where xdg-settings is missing or names none.
var ErrNoDefaultBrowser = errors.New("this computer has no default browser")

// DefaultBrowser is the LaunchServices https handler on macOS, which is
// Safari until the user picks another, and xdg-settings' default-web-browser
// on Linux. The raw identifier names an unsupported browser.
func DefaultBrowser(home string) (Browser, string, error) {
	switch runtime.GOOS {
	case "darwin":
		id, err := launchServicesHTTPSHandler(home)
		if err != nil {
			return "", "", err
		}
		if id == "" {
			return Safari, "com.apple.safari", nil
		}
		browser, ok := macBrowsers[strings.ToLower(id)]
		if !ok {
			return "", id, fmt.Errorf("the default browser (%s) is not one Agenthail can read sign-in cookies from; supported: %s", id, supportedLabels())
		}
		return browser, id, nil
	case "linux":
		entry, err := xdgDefaultBrowser()
		if err != nil || entry == "" {
			return "", "", ErrNoDefaultBrowser
		}
		browser, ok := linuxBrowsers[strings.TrimSuffix(entry, ".desktop")]
		if !ok {
			return "", entry, fmt.Errorf("the default browser (%s) is not one Agenthail can read sign-in cookies from; supported: %s", entry, supportedLabels())
		}
		return browser, entry, nil
	}
	return "", "", fmt.Errorf("finding the default browser is not supported on %s", runtime.GOOS)
}

func launchServicesHTTPSHandler(home string) (string, error) {
	path := launchServicesPlist(home)
	data, err := readMetadata(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not read the default browser from %s: %w", path, err)
	}
	var prefs struct {
		LSHandlers []struct {
			URLScheme string `plist:"LSHandlerURLScheme"`
			RoleAll   string `plist:"LSHandlerRoleAll"`
		} `plist:"LSHandlers"`
	}
	if _, err := plist.Unmarshal(data, &prefs); err != nil {
		return "", fmt.Errorf("could not read the default browser from %s: %w", path, err)
	}
	for _, handler := range prefs.LSHandlers {
		if strings.EqualFold(handler.URLScheme, "https") {
			return handler.RoleAll, nil
		}
	}
	return "", nil
}

func supportedLabels() string {
	labels := make([]string, len(Browsers))
	for i, browser := range Browsers {
		labels[i] = browser.Label()
	}
	return strings.Join(labels, ", ")
}

// DefaultProfile is the default browser's last used profile. On error the
// returned profile still names the browser when it was found.
func DefaultProfile(home string) (Profile, error) {
	browser, _, err := DefaultBrowser(home)
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{Browser: browser}
	switch {
	case browser == Safari:
		profile.Profile, profile.Label = SafariProfile, "Safari"
	case browser == Firefox:
		root := firefoxRoot(home)
		profile.Profile, profile.Label, err = firefoxDefaultProfile(root)
		if errors.Is(err, fs.ErrPermission) {
			return profile, permissionError(profile, root, err)
		}
		if err != nil {
			return profile, fmt.Errorf("Firefox is the default browser, but its profiles could not be read at %s: %v", root, err)
		}
	default:
		root := chromiumRoot(home, browser)
		if root == "" {
			return profile, fmt.Errorf("%s is the default browser, but it is not supported on %s", browser.Label(), runtime.GOOS)
		}
		profile.Profile, profile.Label, err = chromiumLastUsed(root)
		if errors.Is(err, fs.ErrPermission) {
			return profile, permissionError(profile, root, err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return profile, fmt.Errorf("%s is the default browser, but it has no profile data at %s; open it once", browser.Label(), root)
		}
		if err != nil {
			return profile, fmt.Errorf("%s is the default browser, but its profiles could not be read at %s: %v", browser.Label(), root, err)
		}
	}
	return profile, nil
}

func chromiumLastUsed(root string) (string, string, error) {
	data, err := readMetadata(filepath.Join(root, "Local State"))
	if err != nil {
		return "", "", err
	}
	var state struct {
		Profile struct {
			LastUsed  string `json:"last_used"`
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return "", "", fmt.Errorf("Local State is not valid JSON: %w", err)
	}
	dir := state.Profile.LastUsed
	if !validProfileDir(dir) {
		dir = "Default"
	}
	name := state.Profile.InfoCache[dir].Name
	if name == "" {
		name = dir
	}
	return dir, name, nil
}

// Firefox opens the install's default when it recorded one, otherwise the
// profile marked Default=1.
func firefoxDefaultProfile(root string) (string, string, error) {
	data, err := readMetadata(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return "", "", err
	}
	type entry struct{ name, path string }
	var profiles []entry
	var installDefault, markedDefault string
	section := ""
	var current entry
	marked := false
	flush := func() {
		if strings.HasPrefix(section, "[Profile") && current.path != "" {
			profiles = append(profiles, current)
			if marked {
				markedDefault = current.path
			}
		}
		current, marked = entry{}, false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(section, "[Install") && key == "Default" && installDefault == "":
			installDefault = value
		case strings.HasPrefix(section, "[Profile") && key == "Name":
			current.name = value
		case strings.HasPrefix(section, "[Profile") && key == "Path":
			current.path = value
		case strings.HasPrefix(section, "[Profile") && key == "Default":
			marked = value == "1"
		}
	}
	flush()
	chosen := installDefault
	if chosen == "" {
		chosen = markedDefault
	}
	if chosen == "" && len(profiles) == 1 {
		chosen = profiles[0].path
	}
	if chosen == "" {
		return "", "", errors.New("profiles.ini names no default profile")
	}
	for _, profile := range profiles {
		if profile.path == chosen && profile.name != "" {
			return chosen, profile.name, nil
		}
	}
	return chosen, chosen, nil
}
