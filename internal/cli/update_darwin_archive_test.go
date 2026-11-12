package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func darwinInstall(t *testing.T) string {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "agenthail")
	if err := os.MkdirAll(filepath.Join(dataDir, "Agenthail.app", "Contents", "MacOS"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"agenthail": versionScript("v0.2.133"), "sidecar.py": "sidecar", "cookie.mjs": "cookie"} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

func TestADarwinReleaseArchiveInstallIsAnArchiveInstall(t *testing.T) {
	dataDir := darwinInstall(t)
	binary := filepath.Join(dataDir, "agenthail")
	for _, c := range []struct {
		goos, version, want string
	}{
		{"darwin", "v0.2.133", "archive"},
		{"darwin", "v0.2.133-4-gabcdef0", "source"},
		{"darwin", "v0.2.133-dirty", "source"},
		{"darwin", "dev", "source"},
		{"linux", "v0.2.133", "archive"},
	} {
		if got := updateInstallMethod(binary, c.goos, c.version); got != c.want {
			t.Errorf("%s %s: install method %q, want %q", c.goos, c.version, got, c.want)
		}
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "Agenthail.app")); err != nil {
		t.Fatal(err)
	}
	if got := updateInstallMethod(binary, "darwin", "v0.2.133"); got != "source" {
		t.Fatalf("a folder without the release's Mac app is a %q install, want source", got)
	}
	if got := updateInstallMethod("/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail", "darwin", "v0.2.133"); got != "package" {
		t.Fatalf("the package install is %q", got)
	}
}

type darwinUpdateFixture struct {
	app       *App
	dataDir   string
	installs  []string
	env       [][]string
	teams     map[string]string
	installer error
	healthy   error
}

func newDarwinUpdateFixture(t *testing.T) *darwinUpdateFixture {
	t.Helper()
	name := releaseArchiveName("v0.2.134", "darwin", "arm64")
	archive := buildArchive(t, []archiveEntry{
		{name: name + "/"},
		{name: name + "/agenthail", body: versionScript("v0.2.134"), mode: 0755},
		{name: name + "/install.sh", body: "#!/bin/sh\n", mode: 0755},
		{name: name + "/Agenthail.app/"},
		{name: name + "/Agenthail.app/Contents/MacOS/Agenthail", body: "app", mode: 0755},
		{name: name + "/sidecar/"},
		{name: name + "/sidecar/sidecar.py", body: "sidecar", mode: 0644},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.134","html_url":"https://example.test/releases/v0.2.134","assets":[{"name":"%s.tar.gz","browser_download_url":"%s/archive"},{"name":"%s.tar.gz.sha256","browser_download_url":"%s/checksum"}]}`, name, serverURL(r), name, serverURL(r))
		case "/archive":
			w.Write(archive)
		case "/checksum":
			digest := sha256.Sum256(archive)
			fmt.Fprintf(w, "%s  %s.tar.gz\n", hex.EncodeToString(digest[:]), name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	f := &darwinUpdateFixture{dataDir: darwinInstall(t), teams: map[string]string{"agenthail": "TEAM123", "Agenthail.app": "TEAM123"}}
	f.app = &App{Version: "v0.2.133", update: &updateDeps{
		client:     server.Client(),
		releaseURL: server.URL + "/latest",
		executable: func() (string, error) { return filepath.Join(f.dataDir, "agenthail"), nil },
		run:        func(string, ...string) error { return errors.New("unexpected command") },
		output: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
		goos:           "darwin",
		goarch:         "arm64",
		daemonExpected: func() bool { return true },
		serviceCheck:   func() error { return errors.New("the darwin path must not ask systemd") },
		restartDaemon:  func(string) error { return errors.New("the installer restarts the daemon") },
		daemonHealthy:  func() error { return f.healthy },
		signingTeam: func(path string) (string, error) {
			if strings.HasPrefix(path, f.dataDir+string(filepath.Separator)) {
				return "TEAM123", nil
			}
			team, ok := f.teams[filepath.Base(path)]
			if !ok || team == "" {
				return "", fmt.Errorf("%s is not signed", path)
			}
			return team, nil
		},
		runInstaller: func(script string, env []string) error {
			if _, err := os.Stat(script); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(script), "Agenthail.app")); err != nil {
				return fmt.Errorf("the installer runs outside the extracted release: %w", err)
			}
			f.installs, f.env = append(f.installs, filepath.Base(script)), append(f.env, env)
			return f.installer
		},
	}}
	return f
}

func TestADarwinArchiveInstallUpdatesThroughItsReleasesOwnInstaller(t *testing.T) {
	f := newDarwinUpdateFixture(t)
	if _, err := captureStdout(t, func() error { return f.app.Run([]string{"update"}) }); err != nil {
		t.Fatal(err)
	}
	if len(f.installs) != 1 || f.installs[0] != "install.sh" {
		t.Fatalf("installers run: %v", f.installs)
	}
	if len(f.env) != 1 || len(f.env[0]) != 1 || f.env[0][0] != "AGENTHAIL_DATA_DIR="+f.dataDir {
		t.Fatalf("installer env = %v, want this install's data dir", f.env)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.dataDir), ".agenthail-update-*"))
	if len(leftovers) != 0 {
		t.Fatalf("the download was left behind: %v", leftovers)
	}
}

func TestADarwinArchiveUpdateRefusesAReleaseSignedByAnotherTeam(t *testing.T) {
	for name, teams := range map[string]map[string]string{
		"another team's binary": {"agenthail": "OTHER99", "Agenthail.app": "TEAM123"},
		"another team's app":    {"agenthail": "TEAM123", "Agenthail.app": "OTHER99"},
		"an unsigned binary":    {"agenthail": "", "Agenthail.app": "TEAM123"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDarwinUpdateFixture(t)
			f.teams = teams
			_, err := captureStdout(t, func() error { return f.app.Run([]string{"update"}) })
			if err == nil || len(f.installs) != 0 {
				t.Fatalf("err=%v installers run=%v; the release must be refused before it is installed", err, f.installs)
			}
		})
	}
}

func TestADarwinArchiveUpdateReportsAFailedInstallOrAnUnhealthyDaemon(t *testing.T) {
	f := newDarwinUpdateFixture(t)
	f.installer = errors.New("exit status 1")
	_, err := captureStdout(t, func() error { return f.app.Run([]string{"update"}) })
	if err == nil || !strings.Contains(err.Error(), "restores the previous install") {
		t.Fatalf("a failed installer reported %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.dataDir, "agenthail")); string(got) != versionScript("v0.2.133") {
		t.Fatalf("the install changed though the installer failed: %q", got)
	}

	f = newDarwinUpdateFixture(t)
	f.healthy = errors.New("daemon is not running")
	_, err = captureStdout(t, func() error { return f.app.Run([]string{"update"}) })
	if err == nil || !strings.Contains(err.Error(), "daemon is not answering") {
		t.Fatalf("an unhealthy daemon after the install reported %v", err)
	}
}

func TestDoctorNamesADarwinArchiveInstall(t *testing.T) {
	f := newDarwinUpdateFixture(t)
	if got := f.app.installMethod("darwin"); got != "archive" {
		t.Fatalf("doctor's install kind = %q", got)
	}
	f.app.Version = "v0.2.133-2-gabcdef0"
	if got := f.app.installMethod("darwin"); got != "source" {
		t.Fatalf("a source build in the same folder reads %q", got)
	}
}

func TestTheSignerOfAModifiedBinaryIsNotTrusted(t *testing.T) {
	if _, err := os.Stat("/usr/bin/codesign"); err != nil {
		t.Skip("codesign is macOS only")
	}
	binary := filepath.Join(t.TempDir(), "signed")
	if err := os.WriteFile(binary, []byte(versionScript("v0.2.134")), 0755); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--sign", "-", binary).CombinedOutput(); err != nil {
		t.Skipf("cannot sign a fixture here: %s", output)
	}
	if team, err := defaultSigningTeam(binary); err == nil {
		t.Fatalf("an ad hoc signature names team %q, want it refused", team)
	}
	file, err := os.OpenFile(binary, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString("echo changed\n")
	file.Close()
	if team, err := defaultSigningTeam(binary); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a binary changed after signing read team %q, err=%v", team, err)
	}
}
