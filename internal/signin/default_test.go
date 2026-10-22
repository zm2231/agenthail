package signin

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setDefaultBrowser(t *testing.T, home, id string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		writeFile(t, launchServicesPlist(home), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>LSHandlers</key><array>
<dict><key>LSHandlerContentType</key><string>public.html</string><key>LSHandlerRoleAll</key><string>com.example.editor</string></dict>
<dict><key>LSHandlerURLScheme</key><string>https</string><key>LSHandlerRoleAll</key><string>`+id+`</string></dict>
</array></dict></plist>`)
	case "linux":
		original := xdgDefaultBrowser
		t.Cleanup(func() { xdgDefaultBrowser = original })
		xdgDefaultBrowser = func() (string, error) { return id, nil }
	default:
		t.Skipf("no default browser lookup on %s", runtime.GOOS)
	}
}

func browserID(browser Browser) string {
	if runtime.GOOS == "linux" {
		shortest := ""
		for id, candidate := range linuxBrowsers {
			if candidate == browser && (shortest == "" || len(id) < len(shortest)) {
				shortest = id
			}
		}
		if shortest != "" {
			return shortest + ".desktop"
		}
	}
	for id, candidate := range macBrowsers {
		if candidate == browser {
			return id
		}
	}
	return ""
}

func TestAutomaticUsesTheDefaultBrowsersLastUsedProfile(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"last_used":"Profile 2","info_cache":{"Default":{"name":"Personal"},"Profile 2":{"name":"Work"}}}}`)
	writeFile(t, filepath.Join(chrome, "Profile 2", "Network", "Cookies"), "")
	setDefaultBrowser(t, home, browserID(Chrome))

	selection, source, err := Select(home, Notion, Choice{Mode: Automatic})
	if err != nil {
		t.Fatal(err)
	}
	if selection != (Selection{Mode: Automatic, Browser: Chrome, BrowserLabel: "Google Chrome", Profile: "Profile 2", Name: "Work"}) {
		t.Fatalf("selection=%+v", selection)
	}
	if source != (Source{Label: "Work", Browser: Chrome, Dir: filepath.Join(chrome, "Profile 2")}) {
		t.Fatalf("source=%+v", source)
	}

	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"info_cache":{"Default":{"name":"Personal"}}}}`)
	writeFile(t, filepath.Join(chrome, "Default", "Cookies"), "")
	if selection, _, err := Select(home, Notion, Choice{Mode: Automatic}); err != nil || selection.Profile != "Default" || selection.Name != "Personal" {
		t.Fatalf("a browser with no last used profile did not fall back to Default: %+v %v", selection, err)
	}
}

func TestAutomaticFirefoxUsesTheInstallDefaultProfile(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	firefox := firefoxRoot(home)
	writeFile(t, filepath.Join(firefox, "profiles.ini"), "[Profile1]\nName=old\nIsRelative=1\nPath=Profiles/old.default\nDefault=1\n\n[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/abcd.default-release\n\n[Install4F96D1932A9F858E]\nDefault=Profiles/abcd.default-release\nLocked=1\n")
	writeFile(t, filepath.Join(firefox, "Profiles", "abcd.default-release", "cookies.sqlite"), "")
	setDefaultBrowser(t, home, browserID(Firefox))

	selection, source, err := Select(home, Claude, Choice{Mode: Automatic})
	if err != nil || selection.Browser != Firefox || selection.Profile != "Profiles/abcd.default-release" || selection.Name != "default-release" || source.Dir != filepath.Join(firefox, "Profiles", "abcd.default-release") {
		t.Fatalf("selection=%+v source=%+v err=%v", selection, source, err)
	}

	writeFile(t, filepath.Join(firefox, "profiles.ini"), "[Profile1]\nName=old\nIsRelative=1\nPath=Profiles/old.default\nDefault=1\n\n[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/abcd.default-release\n")
	writeFile(t, filepath.Join(firefox, "Profiles", "old.default", "prefs.js"), "")
	if selection, _, err := Select(home, Claude, Choice{Mode: Automatic}); selection.Profile != "Profiles/old.default" || !strings.Contains(fmtErr(err), `profile "Profiles/old.default" has no cookie store`) {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}

func TestAutomaticNamesADefaultBrowserItCannotRead(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	setDefaultBrowser(t, home, "com.example.otherbrowser")
	selection, _, err := Select(home, Notion, Choice{Mode: Automatic})
	var problem ProblemError
	if !errors.As(err, &problem) || selection.Mode != Automatic || selection.Browser != "" {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	for _, want := range []string{"Notion: the default browser (com.example.otherbrowser) is not one Agenthail can read", "agenthail signin use notion <profile|auto|none>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%q missing %q", err, want)
		}
	}

	setDefaultBrowser(t, home, browserID(Brave))
	selection, _, err = Select(home, Notion, Choice{Mode: Automatic})
	if selection.Browser != Brave || !strings.Contains(fmtErr(err), "Brave is the default browser, but it has no profile data") {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}

func TestSafariIsTheDefaultUntilTheUserPicksAnotherBrowser(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchServices is macOS only")
	}
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"), "")
	selection, source, err := Select(home, Notion, Choice{Mode: Automatic})
	if err != nil || selection != (Selection{Mode: Automatic, Browser: Safari, BrowserLabel: "Safari", Profile: SafariProfile, Name: "Safari"}) || source != (Source{Label: "Safari", Browser: Safari}) {
		t.Fatalf("selection=%+v source=%+v err=%v", selection, source, err)
	}
}

func TestLinuxDefaultBrowserComesFromXDGSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("xdg-settings is Linux only")
	}
	original := xdgDefaultBrowser
	t.Cleanup(func() { xdgDefaultBrowser = original })
	xdgDefaultBrowser = func() (string, error) { return "chromium-browser.desktop", nil }
	if browser, _, err := DefaultBrowser(t.TempDir()); err != nil || browser != Chromium {
		t.Fatalf("browser=%q err=%v", browser, err)
	}
	xdgDefaultBrowser = func() (string, error) { return "", errors.New("exit status 1") }
	if _, _, err := DefaultBrowser(t.TempDir()); err == nil || !strings.Contains(err.Error(), "xdg-settings get default-web-browser") {
		t.Fatalf("err=%v", err)
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
