package signin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func requireDesktopRoots(t *testing.T) {
	t.Helper()
	if chromiumRoot(t.TempDir(), Chrome) == "" {
		t.Skipf("no browser profile locations on %s", runtime.GOOS)
	}
}

func browserByName(detected []DetectedBrowser, browser Browser) (DetectedBrowser, bool) {
	for _, candidate := range detected {
		if candidate.Browser == browser {
			return candidate, true
		}
	}
	return DetectedBrowser{}, false
}

func TestDetectListsProfilesWithCookieStores(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"info_cache":{"Default":{"name":"Personal"},"Profile 2":{"name":"Work"},"Profile 3":{"name":"No cookies"},"../escape":{"name":"Bad"}}}}`)
	writeFile(t, filepath.Join(chrome, "Default", "Network", "Cookies"), "")
	writeFile(t, filepath.Join(chrome, "Profile 2", "Cookies"), "")
	firefox := firefoxRoot(home)
	writeFile(t, filepath.Join(firefox, "profiles.ini"), "[General]\nStartWithLastProfile=1\n\n[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/abcd.default-release\n\n[Profile1]\nName=empty\nIsRelative=1\nPath=Profiles/empty\n\n[Install123]\nDefault=Profiles/abcd.default-release\n")
	writeFile(t, filepath.Join(firefox, "Profiles", "abcd.default-release", "cookies.sqlite"), "")

	detected := Detect(context.Background(), home)
	got, ok := browserByName(detected, Chrome)
	if !ok || !got.Available || len(got.Profiles) != 2 || got.Profiles[0] != (DetectedProfile{Profile: "Default", Name: "Personal"}) || got.Profiles[1] != (DetectedProfile{Profile: "Profile 2", Name: "Work"}) {
		t.Fatalf("chrome=%+v", got)
	}
	got, ok = browserByName(detected, Firefox)
	if !ok || len(got.Profiles) != 1 || got.Profiles[0] != (DetectedProfile{Profile: "Profiles/abcd.default-release", Name: "default-release"}) {
		t.Fatalf("firefox=%+v", got)
	}
	if _, ok := browserByName(detected, Brave); ok {
		t.Fatalf("a browser that is not installed is listed: %+v", detected)
	}
}

func TestDetectReportsABlockedBrowserUnavailableWithoutWaiting(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	edge := chromiumRoot(home, Edge)
	if err := os.MkdirAll(edge, 0700); err != nil {
		t.Fatal(err)
	}
	blocking := filepath.Join(edge, "Local State")
	if err := syscall.Mkfifo(blocking, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if writer, err := os.OpenFile(blocking, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			writer.Close()
		}
	})
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"info_cache":{"Default":{"name":"Personal"}}}}`)
	writeFile(t, filepath.Join(chrome, "Default", "Network", "Cookies"), "")
	original := ProbeTimeout
	ProbeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ProbeTimeout = original })

	started := time.Now()
	detected := Detect(context.Background(), home)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("detect waited %s on a blocked browser", elapsed)
	}
	got, ok := browserByName(detected, Edge)
	if !ok || got.Available || !strings.Contains(got.Detail, "did not finish") {
		t.Fatalf("edge=%+v", got)
	}
	if got, ok := browserByName(detected, Chrome); !ok || !got.Available || len(got.Profiles) != 1 {
		t.Fatalf("a blocked browser hid another: %+v", got)
	}
}

func TestDetectOffersSafariOnlyWhenInstalledAndNeverTouchesItsCookies(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Safari is macOS only")
	}
	home := t.TempDir()
	cookies := filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies")
	if err := os.MkdirAll(filepath.Dir(cookies), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(cookies, 0600); err != nil {
		t.Fatal(err)
	}
	original := safariApp
	t.Cleanup(func() { safariApp = original })
	safariApp = t.TempDir()
	got, ok := browserByName(Detect(context.Background(), home), Safari)
	if !ok || !got.Available || len(got.Profiles) != 1 || got.Profiles[0].Profile != SafariProfile {
		t.Fatalf("safari=%+v", got)
	}
	safariApp = filepath.Join(t.TempDir(), "missing")
	if _, ok := browserByName(Detect(context.Background(), home), Safari); ok {
		t.Fatal("Safari listed when it is not installed")
	}
}

func TestSourceForLocatesTheProfileDirectory(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	chromeDir := filepath.Join(chromiumRoot(home, Chrome), "Profile 2")
	firefoxDir := filepath.Join(firefoxRoot(home), "Profiles", "abcd.default-release")
	for _, dir := range []string{chromeDir, firefoxDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		profile Profile
		dir     string
	}{
		{Profile{ID: "sp_1", Browser: Chrome, Profile: "Profile 2"}, chromeDir},
		{Profile{ID: "sp_2", Browser: Firefox, Profile: "Profiles/abcd.default-release"}, firefoxDir},
		{Profile{ID: "sp_3", Browser: Safari, Profile: SafariProfile}, ""},
	} {
		source, err := SourceFor(home, test.profile)
		if err != nil || source != (Source{ProfileID: test.profile.ID, Browser: test.profile.Browser, Dir: test.dir}) {
			t.Fatalf("profile=%+v source=%+v err=%v", test.profile, source, err)
		}
	}
	for _, profile := range []Profile{
		{ID: "sp_4", Label: "Gone", Browser: Chrome, Profile: "Profile 9"},
		{ID: "sp_5", Label: "Escape", Browser: Chrome, Profile: "../Profile 2"},
		{ID: "sp_6", Label: "Unknown", Browser: "opera", Profile: "Default"},
	} {
		if _, err := SourceFor(home, profile); err == nil {
			t.Fatalf("profile=%+v resolved", profile)
		}
	}
}

func TestUnsetConsumerNamesTheCommandAndSettingsPage(t *testing.T) {
	_, err := Resolve(nil, Target{Consumer: Claude})
	if !IsNotConfigured(err) {
		t.Fatalf("err=%v", err)
	}
	for _, want := range []string{"Claude Remote Control: choose a browser sign-in profile", "agenthail signin use claude <profile>", "agenthail signin list", "Settings > Browser Sign-in"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err=%q missing %q", err, want)
		}
	}
}
