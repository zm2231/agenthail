package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

func updateFixture(t *testing.T, current string, checksum string) (*App, *[]recordedCommand, *int) {
	t.Helper()
	pkg := []byte("signed-package-fixture")
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","html_url":"https://example.test/releases/v0.2.0","assets":[{"name":"Agenthail-v0.2.0-arm64.pkg","browser_download_url":"%s/pkg"},{"name":"Agenthail-v0.2.0-arm64.pkg.sha256","browser_download_url":"%s/checksum"}]}`, serverURL(r), serverURL(r))
		case "/pkg":
			downloads++
			w.Write(pkg)
		case "/checksum":
			downloads++
			value := checksum
			if value == "" {
				digest := sha256.Sum256(pkg)
				value = hex.EncodeToString(digest[:])
			}
			fmt.Fprintf(w, "%s  Agenthail-v0.2.0-arm64.pkg\n", value)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	commands := []recordedCommand{}
	deps := &updateDeps{
		client:          server.Client(),
		releaseURL:      server.URL + "/latest",
		installerTeamID: "Q5Y75DVV4M",
		executable:      func() (string, error) { return "/Library/Application Support/Agenthail/agenthail", nil },
		run: func(name string, args ...string) error {
			commands = append(commands, recordedCommand{name: name, args: append([]string(nil), args...)})
			return nil
		},
		output: func(name string, args ...string) ([]byte, error) {
			commands = append(commands, recordedCommand{name: name, args: append([]string(nil), args...)})
			return []byte("Developer ID Installer: Agenthail Release (Q5Y75DVV4M)"), nil
		},
		goos:   "darwin",
		goarch: "arm64",
	}
	return &App{Version: current, update: deps}, &commands, &downloads
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestDefaultUpdateDepsUsesEmbeddedReleaseTrust(t *testing.T) {
	previousURL := latestReleaseURL
	previousTeam := expectedInstallerTeamID
	latestReleaseURL = "https://api.example.test/releases/latest"
	expectedInstallerTeamID = " AAAAAAAAAA "
	t.Cleanup(func() {
		latestReleaseURL = previousURL
		expectedInstallerTeamID = previousTeam
	})
	deps := defaultUpdateDeps()
	if deps.releaseURL != latestReleaseURL || deps.installerTeamID != "AAAAAAAAAA" {
		t.Fatalf("releaseURL=%q installerTeamID=%q", deps.releaseURL, deps.installerTeamID)
	}
}

func TestUpdateCheckReportsAvailableWithoutDownloading(t *testing.T) {
	app, commands, downloads := updateFixture(t, "v0.1.7", "")
	output, err := captureStdout(t, func() error { return app.Run([]string{"update", "--check", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var status updateStatus
	if json.Unmarshal([]byte(output), &status) != nil || !status.Available || status.Latest != "v0.2.0" || status.InstallMethod != "package" {
		t.Fatalf("status=%+v output=%s", status, output)
	}
	if *downloads != 0 || len(*commands) != 0 {
		t.Fatalf("downloads=%d commands=%v", *downloads, *commands)
	}
}

func TestUpdateInstallsVerifiedPackage(t *testing.T) {
	app, commands, downloads := updateFixture(t, "v0.1.7", "")
	output, err := captureStdout(t, func() error { return app.Run([]string{"upgrade", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if *downloads != 2 || len(*commands) != 3 {
		t.Fatalf("downloads=%d commands=%v", *downloads, *commands)
	}
	want := []string{"/usr/sbin/pkgutil", "/usr/sbin/spctl", "/usr/bin/sudo"}
	for i, command := range *commands {
		if command.name != want[i] {
			t.Fatalf("command[%d]=%+v", i, command)
		}
	}
	if got := (*commands)[2].args; len(got) != 5 || got[0] != "/usr/sbin/installer" || got[1] != "-pkg" || got[3] != "-target" || got[4] != "/" {
		t.Fatalf("installer args=%q", got)
	}
	var status updateStatus
	if json.Unmarshal([]byte(output), &status) != nil || status.Available || status.Current != "v0.2.0" {
		t.Fatalf("status=%+v output=%s", status, output)
	}
}

func TestUpdateRejectsChecksumMismatchBeforeVerification(t *testing.T) {
	app, commands, _ := updateFixture(t, "v0.1.7", strings.Repeat("0", 64))
	err := app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "checksum does not match") {
		t.Fatalf("err=%v", err)
	}
	if len(*commands) != 0 {
		t.Fatalf("commands=%v", *commands)
	}
}

func TestUpdateRejectsPackageFromAnotherDeveloper(t *testing.T) {
	app, commands, _ := updateFixture(t, "v0.1.7", "")
	app.update.output = func(name string, args ...string) ([]byte, error) {
		*commands = append(*commands, recordedCommand{name: name, args: append([]string(nil), args...)})
		return []byte("Developer ID Installer: OTHER DEVELOPER (AAAAAAAAAA)"), nil
	}
	err := app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "not from the configured Apple developer team") {
		t.Fatalf("err=%v", err)
	}
	if len(*commands) != 1 || (*commands)[0].name != "/usr/sbin/pkgutil" {
		t.Fatalf("commands=%v", *commands)
	}
}

func TestUpdateRejectsPackageWhenInstallerTrustIsUnconfigured(t *testing.T) {
	app, commands, downloads := updateFixture(t, "v0.1.7", "")
	app.update.installerTeamID = ""
	err := app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "updater trust is not configured") {
		t.Fatalf("err=%v", err)
	}
	if *downloads != 0 || len(*commands) != 0 {
		t.Fatalf("downloads=%d commands=%v", *downloads, *commands)
	}
}

func TestUpdateDoesNotDowngradeNewerLocalBuild(t *testing.T) {
	app, commands, downloads := updateFixture(t, "v0.3.0-local", "")
	output, err := captureStdout(t, func() error { return app.Run([]string{"update"}) })
	if err != nil || !strings.Contains(output, "up to date") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if *downloads != 0 || len(*commands) != 0 {
		t.Fatalf("downloads=%d commands=%v", *downloads, *commands)
	}
}

func TestUpdateRejectsSourceInstall(t *testing.T) {
	app := &App{Version: "v0.1.7", update: &updateDeps{
		executable: func() (string, error) { return "/tmp/agenthail", nil },
	}}
	err := app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "git pull and ./install.sh") {
		t.Fatalf("err=%v", err)
	}
}

func TestUpdateUsesHomebrewForHomebrewInstall(t *testing.T) {
	commands := []recordedCommand{}
	app := &App{Version: "v0.1.7", update: &updateDeps{
		executable: func() (string, error) { return "/opt/homebrew/Cellar/agenthail/0.1.7/bin/agenthail", nil },
		run: func(name string, args ...string) error {
			commands = append(commands, recordedCommand{name: name, args: append([]string(nil), args...)})
			return nil
		},
	}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"update"}) })
	if err != nil || !strings.Contains(output, "updated with homebrew") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if len(commands) != 2 || commands[0].name != "/opt/homebrew/bin/brew" || commands[0].args[0] != "update" || commands[1].args[0] != "upgrade" {
		t.Fatalf("commands=%v", commands)
	}
}

func TestUpdateChecksHomebrewWithJSON(t *testing.T) {
	commands := []recordedCommand{}
	app := &App{Version: "v0.1.7", update: &updateDeps{
		executable: func() (string, error) { return "/opt/homebrew/Cellar/agenthail/0.1.7/bin/agenthail", nil },
		output: func(name string, args ...string) ([]byte, error) {
			commands = append(commands, recordedCommand{name: name, args: append([]string(nil), args...)})
			return []byte(`{"formulae":[{"name":"agenthail","current_version":"0.2.0"}],"casks":[]}`), exec.Command("/usr/bin/false").Run()
		},
	}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"update", "--check", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var status updateStatus
	if json.Unmarshal([]byte(output), &status) != nil || !status.Available || status.Latest != "0.2.0" {
		t.Fatalf("status=%+v output=%s", status, output)
	}
	if len(commands) != 1 || commands[0].args[1] != "--json=v2" {
		t.Fatalf("commands=%v", commands)
	}
}

func TestUpdateChecksHomebrewWithHumanOutputOnExitOne(t *testing.T) {
	app := &App{Version: "v0.1.7", update: &updateDeps{
		executable: func() (string, error) { return "/opt/homebrew/Cellar/agenthail/0.1.7/bin/agenthail", nil },
		output: func(string, ...string) ([]byte, error) {
			return []byte(`{"formulae":[{"current_version":"0.2.0"}]}`), exec.Command("/usr/bin/false").Run()
		},
	}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"update", "--check"}) })
	if err != nil || !strings.Contains(output, "0.2.0 is available") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestUpdateRejectsUnexpectedHomebrewFailure(t *testing.T) {
	app := &App{Version: "v0.1.7", update: &updateDeps{
		executable: func() (string, error) { return "/opt/homebrew/Cellar/agenthail/0.1.7/bin/agenthail", nil },
		output: func(string, ...string) ([]byte, error) {
			return nil, exec.Command("/bin/sh", "-c", "exit 2").Run()
		},
	}}
	err := app.Run([]string{"update", "--check"})
	if err == nil || !strings.Contains(err.Error(), "check Homebrew update") {
		t.Fatalf("err=%v", err)
	}
}

func TestUpdateVersionComparison(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{"v0.2.0", "v0.1.9", 1},
		{"v0.1.7", "v0.1.7-local", 0},
		{"v1.0.0", "v1.0.1", -1},
	}
	for _, test := range tests {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareVersions(%q, %q)=%d want=%d", test.left, test.right, got, test.want)
		}
	}
}

func TestUpdateHelpWorksForBothNames(t *testing.T) {
	for _, command := range []string{"update", "upgrade"} {
		output, err := captureStdout(t, func() error { return (&App{}).Run([]string{command, "--help"}) })
		if err != nil || !strings.Contains(output, "agenthail update") {
			t.Fatalf("command=%s output=%q err=%v", command, output, err)
		}
	}
}
