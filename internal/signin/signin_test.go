package signin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

func TestDetectRefusesSpecialFilesWithoutBlocking(t *testing.T) {
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
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"info_cache":{"Default":{"name":"Personal"}}}}`)
	writeFile(t, filepath.Join(chrome, "Default", "Network", "Cookies"), "")

	started := time.Now()
	detected := Detect(context.Background(), home)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("detect waited %s on a FIFO", elapsed)
	}
	got, ok := browserByName(detected, Edge)
	if !ok || got.Available || !strings.Contains(got.Detail, "not a regular file") {
		t.Fatalf("edge=%+v", got)
	}
	if got, ok := browserByName(detected, Chrome); !ok || !got.Available || len(got.Profiles) != 1 {
		t.Fatalf("a refused browser hid another: %+v", got)
	}
}

func TestDetectRunsOneProbePerBrowserUntilAStuckOneReturns(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	original, originalTimeout := probeBrowser, ProbeTimeout
	t.Cleanup(func() { probeBrowser, ProbeTimeout = original, originalTimeout })
	ProbeTimeout = 100 * time.Millisecond
	probeBrowser = func(home string, browser Browser) *DetectedBrowser {
		if browser != Edge {
			return nil
		}
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return &DetectedBrowser{Browser: Edge, Label: Edge.Label(), Available: true, Profiles: []DetectedProfile{}}
	}
	home := t.TempDir()

	started := time.Now()
	got, ok := browserByName(Detect(context.Background(), home), Edge)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("detect waited %s on a stuck probe", elapsed)
	}
	if !ok || got.Available || !strings.Contains(got.Detail, "did not finish") {
		t.Fatalf("edge=%+v", got)
	}
	got, _ = browserByName(Detect(context.Background(), home), Edge)
	if got.Available || !strings.Contains(got.Detail, "has not finished") {
		t.Fatalf("edge=%+v", got)
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("a stuck probe was started again: calls=%d", calls)
	}
	mu.Unlock()

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		probes.Lock()
		running := len(probes.running)
		probes.Unlock()
		if running == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d probes still running after release", running)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, _ := browserByName(Detect(context.Background(), home), Edge); !got.Available {
		t.Fatalf("edge=%+v", got)
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
	writeFile(t, filepath.Join(chromeDir, "Network", "Cookies"), "")
	writeFile(t, filepath.Join(firefoxDir, "cookies.sqlite"), "")
	writeFile(t, filepath.Join(chromiumRoot(home, Chrome), "Profile 3", "Preferences"), "{}")
	for _, test := range []struct {
		profile Profile
		dir     string
	}{
		{Profile{ID: "sp_1", Browser: Chrome, Profile: "Profile 2"}, chromeDir},
		{Profile{ID: "sp_2", Browser: Firefox, Profile: "Profiles/abcd.default-release"}, firefoxDir},
	} {
		source, err := SourceFor(home, test.profile)
		if err != nil || source != (Source{ProfileID: test.profile.ID, Browser: test.profile.Browser, Dir: test.dir}) {
			t.Fatalf("profile=%+v source=%+v err=%v", test.profile, source, err)
		}
	}
	for _, test := range []struct {
		profile Profile
		want    string
	}{
		{Profile{ID: "sp_4", Label: "Gone", Browser: Chrome, Profile: "Profile 9"}, "no longer exists"},
		{Profile{ID: "sp_5", Label: "Escape", Browser: Chrome, Profile: "../Profile 2"}, "invalid"},
		{Profile{ID: "sp_6", Label: "Unknown", Browser: "opera", Profile: "Default"}, "unsupported"},
		{Profile{ID: "sp_7", Label: "Empty", Browser: Chrome, Profile: "Profile 3"}, `profile "Profile 3" has no cookie store`},
	} {
		if _, err := SourceFor(home, test.profile); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("profile=%+v err=%v, want %q", test.profile, err, test.want)
		}
	}
}

func TestUnreadableCookieStoreIsAPermissionProblemNotAnEmptyBrowser(t *testing.T) {
	requireDesktopRoots(t)
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	home := t.TempDir()
	cookies := filepath.Join(chromiumRoot(home, Chrome), "Default", "Network", "Cookies")
	writeFile(t, cookies, "")
	if err := os.Chmod(cookies, 0); err != nil {
		t.Fatal(err)
	}
	_, err := SourceFor(home, Profile{ID: "sp_1", Browser: Chrome, Profile: "Default"})
	if err == nil || !strings.Contains(err.Error(), "permission denied reading Google Chrome data at "+cookies) || strings.Contains(err.Error(), "no cookie") {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "make it readable by the user that runs Agenthail") || strings.Contains(err.Error(), "Full Disk Access") {
		t.Fatalf("a file mode denial names the wrong fix: %v", err)
	}
	lockedDir := filepath.Join(chromiumRoot(home, Chrome), "Profile 2")
	writeFile(t, filepath.Join(lockedDir, "Cookies"), "")
	if err := os.Chmod(filepath.Dir(lockedDir), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(lockedDir), 0o700) })
	if _, err := SourceFor(home, Profile{ID: "sp_2", Browser: Chrome, Profile: "Profile 2"}); err == nil || !strings.Contains(err.Error(), "permission denied reading Google Chrome data at "+lockedDir) {
		t.Fatalf("a profile behind an unreadable folder was reported missing: %v", err)
	}
}

func TestSafariCookieStoreBehindAContainerPermissionNamesFullDiskAccess(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Safari is macOS only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	home := t.TempDir()
	if _, err := SourceFor(home, Profile{Browser: Safari, Profile: SafariProfile}); err == nil || !strings.Contains(err.Error(), "Safari has no cookie store") {
		t.Fatalf("err=%v", err)
	}
	store := filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies")
	writeFile(t, store, "")
	source, err := SourceFor(home, Profile{ID: "sp_1", Browser: Safari, Profile: SafariProfile})
	if err != nil || source != (Source{ProfileID: "sp_1", Browser: Safari}) {
		t.Fatalf("source=%+v err=%v", source, err)
	}
	if err := os.Chmod(store, 0); err != nil {
		t.Fatal(err)
	}
	_, err = SourceFor(home, Profile{Browser: Safari, Profile: SafariProfile})
	if err == nil || !strings.Contains(err.Error(), "permission denied reading Safari data") || strings.Contains(err.Error(), "no cookie") {
		t.Fatalf("err=%v", err)
	}
}

// macOS privacy protection refuses reads with EPERM. A sandbox deny rule
// refuses the same way, so the check runs again inside one.
func TestCookieStoreRefusedByMacOSPrivacyNamesFullDiskAccess(t *testing.T) {
	if home := os.Getenv("AGENTHAIL_SIGNIN_SANDBOXED_HOME"); home != "" {
		_, err := SourceFor(home, Profile{Browser: Safari, Profile: SafariProfile})
		fmt.Printf("RESULT %v\n", err)
		return
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS privacy protection only")
	}
	sandbox, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec is unavailable")
	}
	home := t.TempDir()
	store := filepath.Join(home, "Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies")
	writeFile(t, store, "")
	resolved, err := filepath.EvalSymlinks(store)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(sandbox, "-p", `(version 1)(allow default)(deny file-read* (literal "`+resolved+`"))`, os.Args[0], "-test.run=^TestCookieStoreRefusedByMacOSPrivacyNamesFullDiskAccess$")
	command.Env = append(os.Environ(), "AGENTHAIL_SIGNIN_SANDBOXED_HOME="+home)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed run failed: %v: %s", err, output)
	}
	for _, want := range []string{"RESULT permission denied reading Safari data at " + store, "System Settings > Privacy & Security > Full Disk Access"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("output=%s missing %q", output, want)
		}
	}
}

func TestOffConsumerSaysHowToTurnItBackOn(t *testing.T) {
	for _, err := range []error{
		func() error { _, err := Resolve(nil, Target{Consumer: Claude}); return err }(),
		func() error { _, _, err := Select(t.TempDir(), Claude, Choice{Mode: Off}); return err }(),
	} {
		if !IsOff(err) {
			t.Fatalf("err=%v", err)
		}
		for _, want := range []string{"Claude Remote Control: browser sign-in is turned off", "agenthail signin use claude auto", "Settings > Browser Sign-in"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("err=%q missing %q", err, want)
			}
		}
	}
}

func TestCandidatesListTheChoiceFirstThenEveryReadableProfile(t *testing.T) {
	requireDesktopRoots(t)
	home := t.TempDir()
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Default", "Cookies"), "")
	writeFile(t, filepath.Join(chrome, "Profile 1", "Cookies"), "")
	personal := Profile{ID: "sp_personal", Label: "personal", Browser: Chrome, Profile: "Default"}
	work := Profile{ID: "sp_work", Label: "work", Browser: Chrome, Profile: "Profile 1"}
	gone := Profile{ID: "sp_gone", Label: "gone", Browser: Chrome, Profile: "Profile 9"}
	profiles := []Profile{gone, personal, work}

	sources, err := CandidatesFor(home, Claude, Choice{Mode: Chosen, Profile: &work}, profiles)
	if err != nil || len(sources) != 2 || sources[0].ProfileID != "sp_work" || sources[1].ProfileID != "sp_personal" {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
	sources, err = CandidatesFor(home, Claude, Choice{Mode: Chosen, Profile: &gone}, profiles)
	if err != nil || len(sources) != 2 || sources[0].ProfileID != "sp_personal" || sources[1].ProfileID != "sp_work" {
		t.Fatalf("an unreadable choice still leaves the readable profiles: sources=%+v err=%v", sources, err)
	}
	if _, err := CandidatesFor(home, Claude, Choice{Mode: Chosen, Profile: &gone}, []Profile{gone}); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("with nothing readable the choice's problem is the answer: err=%v", err)
	}
	if sources, err := CandidatesFor(home, Claude, Choice{Mode: Off}, profiles); !IsOff(err) || sources != nil {
		t.Fatalf("a consumer turned off reads no profile: sources=%+v err=%v", sources, err)
	}
}

func TestCandidatesListTheAutomaticProfileOnceWhenItIsAlsoAdded(t *testing.T) {
	requireDesktopRoots(t)
	if runtime.GOOS != "darwin" {
		t.Skip("LaunchServices is macOS only")
	}
	home := t.TempDir()
	chrome := chromiumRoot(home, Chrome)
	writeFile(t, filepath.Join(chrome, "Local State"), `{"profile":{"last_used":"Default","info_cache":{"Default":{"name":"Personal"},"Profile 1":{"name":"Work"}}}}`)
	writeFile(t, filepath.Join(chrome, "Default", "Cookies"), "")
	writeFile(t, filepath.Join(chrome, "Profile 1", "Cookies"), "")
	setDefaultBrowser(t, home, browserID(Chrome))
	profiles := []Profile{{ID: "sp_personal", Label: "personal", Browser: Chrome, Profile: "Default"}, {ID: "sp_work", Label: "work", Browser: Chrome, Profile: "Profile 1"}}

	sources, err := CandidatesFor(home, Claude, Choice{Mode: Automatic}, profiles)
	if err != nil || len(sources) != 2 || sources[0].Dir != filepath.Join(chrome, "Default") || sources[1].ProfileID != "sp_work" {
		t.Fatalf("one cookie store is one candidate: sources=%+v err=%v", sources, err)
	}
}
