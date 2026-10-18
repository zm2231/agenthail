package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

type archiveEntry struct {
	name string
	body string
	mode int64
}

func versionScript(version string) string {
	return "#!/bin/sh\nprintf '%s\\n' '{\"version\":\"" + version + "\"}'\n"
}

func buildArchive(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(entry.name, "/") {
			header = &tar.Header{Name: entry.name, Mode: 0755, Typeflag: tar.TypeDir}
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func releaseArchive(t *testing.T, tag string) []byte {
	root := linuxArchiveName(tag, "amd64")
	return buildArchive(t, []archiveEntry{
		{name: root + "/"},
		{name: root + "/agenthail", body: versionScript(tag), mode: 0755},
		{name: root + "/README.md", body: "readme", mode: 0644},
		{name: root + "/skills/"},
		{name: root + "/skills/agenthail-operations/SKILL.md", body: "skill " + tag, mode: 0644},
	})
}

type archiveUpdateFixture struct {
	app      *App
	dataDir  string
	restarts []string
}

func newArchiveUpdateFixture(t *testing.T, archive []byte) *archiveUpdateFixture {
	t.Helper()
	name := linuxArchiveName("v0.2.0", "amd64")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","html_url":"https://example.test/releases/v0.2.0","assets":[{"name":"%s.tar.gz","browser_download_url":"%s/archive"},{"name":"%s.tar.gz.sha256","browser_download_url":"%s/checksum"}]}`, name, serverURL(r), name, serverURL(r))
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
	dataDir := filepath.Join(t.TempDir(), "agenthail")
	if err := os.MkdirAll(filepath.Join(dataDir, "skills", "agenthail-operations"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agenthail"), []byte(versionScript("v0.1.7")), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "skills", "agenthail-operations", "SKILL.md"), []byte("skill v0.1.7"), 0644); err != nil {
		t.Fatal(err)
	}
	fixture := &archiveUpdateFixture{dataDir: dataDir}
	deps := &updateDeps{
		client:     server.Client(),
		releaseURL: server.URL + "/latest",
		executable: func() (string, error) { return filepath.Join(dataDir, "agenthail"), nil },
		run:        func(string, ...string) error { return errors.New("unexpected command") },
		output: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
		goos:           "linux",
		goarch:         "amd64",
		daemonExpected: func() bool { return true },
		serviceCheck:   func() error { return nil },
		restartDaemon: func(binary string) error {
			data, err := os.ReadFile(binary)
			if err != nil {
				return err
			}
			fixture.restarts = append(fixture.restarts, string(data))
			return nil
		},
		daemonHealthy: func() error { return nil },
	}
	fixture.app = &App{Version: "v0.1.7", update: deps}
	return fixture
}

func (f *archiveUpdateFixture) installedVersion(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dataDir, "agenthail"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLinuxUpdateInstallsArchiveAndRestartsDaemon(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	if _, err := captureStdout(t, func() error { return fixture.app.Run([]string{"update", "--json"}) }); err != nil {
		t.Fatal(err)
	}
	if got := fixture.installedVersion(t); got != versionScript("v0.2.0") {
		t.Fatalf("installed=%q", got)
	}
	skill, _ := os.ReadFile(filepath.Join(fixture.dataDir, "skills", "agenthail-operations", "SKILL.md"))
	if string(skill) != "skill v0.2.0" {
		t.Fatalf("skill=%q", skill)
	}
	if len(fixture.restarts) != 1 || fixture.restarts[0] != versionScript("v0.2.0") {
		t.Fatalf("restarts=%q", fixture.restarts)
	}
	if _, err := os.Stat(fixture.dataDir + ".previous"); !os.IsNotExist(err) {
		t.Fatalf("previous install kept after a healthy restart: %v", err)
	}
}

func TestLinuxUpdateRollsBackWhenRestartedDaemonDoesNotAnswer(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	healthChecks := 0
	fixture.app.update.daemonHealthy = func() error {
		healthChecks++
		if healthChecks == 1 {
			return errors.New("daemon did not answer on its API")
		}
		return nil
	}
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "rolled back") || !strings.Contains(err.Error(), "daemon did not answer on its API") {
		t.Fatalf("err=%v", err)
	}
	if got := fixture.installedVersion(t); got != versionScript("v0.1.7") {
		t.Fatalf("installed after rollback=%q", got)
	}
	if len(fixture.restarts) != 2 || fixture.restarts[0] != versionScript("v0.2.0") || fixture.restarts[1] != versionScript("v0.1.7") {
		t.Fatalf("restarts=%q", fixture.restarts)
	}
	for _, leftover := range []string{".previous", ".failed"} {
		if _, err := os.Stat(fixture.dataDir + leftover); !os.IsNotExist(err) {
			t.Fatalf("%s left behind: %v", leftover, err)
		}
	}
}

func TestLinuxUpdateKeepsPreviousInstallWhenRollbackRestartFails(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	fixture.app.update.daemonHealthy = func() error { return errors.New("no answer") }
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "restored the previous install, but its daemon did not come back") {
		t.Fatalf("err=%v", err)
	}
	if got := fixture.installedVersion(t); got != versionScript("v0.1.7") {
		t.Fatalf("installed=%q", got)
	}
}

func TestLinuxUpdateSkipsRestartWhenNoDaemonIsExpected(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	fixture.app.update.daemonExpected = func() bool { return false }
	if _, err := captureStdout(t, func() error { return fixture.app.Run([]string{"update"}) }); err != nil {
		t.Fatal(err)
	}
	if len(fixture.restarts) != 0 || fixture.installedVersion(t) != versionScript("v0.2.0") {
		t.Fatalf("restarts=%q installed=%q", fixture.restarts, fixture.installedVersion(t))
	}
}

func TestLinuxUpdateRefusesDirectoryWithForeignFiles(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	if err := os.WriteFile(filepath.Join(fixture.dataDir, "notes.txt"), []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "notes.txt, which is not part of the Agenthail release archive") {
		t.Fatalf("err=%v", err)
	}
	if fixture.installedVersion(t) != versionScript("v0.1.7") {
		t.Fatal("install changed")
	}
}

func TestLinuxUpdateRejectsArchiveEntriesOutsideItsRoot(t *testing.T) {
	root := linuxArchiveName("v0.2.0", "amd64")
	archive := buildArchive(t, []archiveEntry{
		{name: root + "/agenthail", body: versionScript("v0.2.0"), mode: 0755},
		{name: root + "/../escape", body: "x", mode: 0644},
	})
	fixture := newArchiveUpdateFixture(t, archive)
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "is outside") {
		t.Fatalf("err=%v", err)
	}
	if fixture.installedVersion(t) != versionScript("v0.1.7") || len(fixture.restarts) != 0 {
		t.Fatal("install changed")
	}
}

func TestLinuxUpdateRejectsBinaryWithWrongVersion(t *testing.T) {
	root := linuxArchiveName("v0.2.0", "amd64")
	fixture := newArchiveUpdateFixture(t, buildArchive(t, []archiveEntry{{name: root + "/agenthail", body: versionScript("v0.1.9"), mode: 0755}}))
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), `reports version "v0.1.9", want v0.2.0`) {
		t.Fatalf("err=%v", err)
	}
	if fixture.installedVersion(t) != versionScript("v0.1.7") {
		t.Fatal("install changed")
	}
}

func TestLinuxUpdateRejectsDevelopmentBuild(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	fixture.app.Version = "dev"
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "update this source install") {
		t.Fatalf("err=%v", err)
	}
}

func TestLinuxUpdateRefusesToSwapWhenServiceManagerIsUnreachable(t *testing.T) {
	fixture := newArchiveUpdateFixture(t, releaseArchive(t, "v0.2.0"))
	fixture.app.update.serviceCheck = func() error {
		return errors.New("agenthail.service is installed but the systemd user manager is unreachable")
	}
	err := fixture.app.Run([]string{"update"})
	if err == nil || !strings.Contains(err.Error(), "refusing to update without a reachable service manager") {
		t.Fatalf("err=%v", err)
	}
	if fixture.installedVersion(t) != versionScript("v0.1.7") || len(fixture.restarts) != 0 {
		t.Fatalf("install changed: restarts=%q", fixture.restarts)
	}
	if _, err := os.Stat(fixture.dataDir + ".previous"); !os.IsNotExist(err) {
		t.Fatalf("previous install moved: %v", err)
	}
}
