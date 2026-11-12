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

func teamSigned(string) bool { return true }

func adHoc(string) bool { return false }

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
		if got := updateInstallMethod(binary, c.goos, c.version, teamSigned); got != c.want {
			t.Errorf("%s %s: install method %q, want %q", c.goos, c.version, got, c.want)
		}
	}
	if got := updateInstallMethod(binary, "darwin", "v0.2.133", adHoc); got != "source" {
		t.Fatalf("a source build of an exact tag, signed ad hoc, is a %q install, want source", got)
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "Agenthail.app")); err != nil {
		t.Fatal(err)
	}
	if got := updateInstallMethod(binary, "darwin", "v0.2.133", teamSigned); got != "source" {
		t.Fatalf("a folder without the release's Mac app is a %q install, want source", got)
	}
	if got := updateInstallMethod("/Library/Application Support/Agenthail/Agenthail Daemon.app/Contents/MacOS/agenthail", "darwin", "v0.2.133", teamSigned); got != "package" {
		t.Fatalf("the package install is %q", got)
	}
}

type darwinUpdateFixture struct {
	app       *App
	dataDir   string
	wrapper   string
	installs  []string
	env       [][]string
	teams     map[string]string
	installer func(dataDir string) error
	healthy   []error
	restarts  []string
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
	wrapperDir := filepath.Join(t.TempDir(), "custom bin")
	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
		t.Fatal(err)
	}
	f.wrapper = filepath.Join(wrapperDir, "agenthail")
	if err := os.WriteFile(f.wrapper, []byte("#!/usr/bin/env bash\n# agenthail-managed-wrapper-v1\nexec "+filepath.Join(f.dataDir, "agenthail")+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
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
		restartDaemon: func(binary string) error {
			data, err := os.ReadFile(binary)
			f.restarts = append(f.restarts, string(data))
			return err
		},
		daemonHealthy: func() error {
			if len(f.healthy) == 0 {
				return nil
			}
			next := f.healthy[0]
			f.healthy = f.healthy[1:]
			return next
		},
		copyTree:    defaultCopyTree,
		wrapperDirs: func() []string { return []string{t.TempDir(), wrapperDir} },
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
			if f.installer != nil {
				return f.installer(f.dataDir)
			}
			return os.WriteFile(filepath.Join(f.dataDir, "agenthail"), []byte(versionScript("v0.2.134")), 0755)
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
	if len(f.env) != 1 || strings.Join(f.env[0], "|") != "AGENTHAIL_DATA_DIR="+f.dataDir+"|AGENTHAIL_INSTALL_DIR="+filepath.Dir(f.wrapper) {
		t.Fatalf("installer env = %v, want this install's data dir and its command's folder", f.env)
	}
	if len(f.restarts) != 0 {
		t.Fatalf("restarted %d times after a healthy install", len(f.restarts))
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

func TestADarwinArchiveUpdateRollsBackAFailureAfterTheInstallerActivatedIt(t *testing.T) {
	activate := func(dataDir string) error {
		if err := os.WriteFile(filepath.Join(dataDir, "agenthail"), []byte(versionScript("v0.2.134")), 0755); err != nil {
			return err
		}
		return os.Remove(filepath.Join(dataDir, "sidecar.py"))
	}
	for name, setup := range map[string]func(*darwinUpdateFixture){
		"the installer fails after activating": func(f *darwinUpdateFixture) {
			f.installer = func(dataDir string) error {
				if err := activate(dataDir); err != nil {
					return err
				}
				os.WriteFile(f.wrapper, []byte("replaced"), 0755)
				return errors.New("exit status 1")
			}
		},
		"the new daemon does not answer": func(f *darwinUpdateFixture) {
			f.installer = activate
			f.healthy = []error{errors.New("daemon is not running")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDarwinUpdateFixture(t)
			wrapper, _ := os.ReadFile(f.wrapper)
			setup(f)
			_, err := captureStdout(t, func() error { return f.app.Run([]string{"update"}) })
			if err == nil || !strings.Contains(err.Error(), "rolled back") {
				t.Fatalf("update reported %v, want a rollback", err)
			}
			if got, _ := os.ReadFile(filepath.Join(f.dataDir, "agenthail")); string(got) != versionScript("v0.2.133") {
				t.Fatalf("after the rollback the binary is %q", got)
			}
			if _, err := os.Stat(filepath.Join(f.dataDir, "sidecar.py")); err != nil {
				t.Fatalf("the rollback lost a file of the previous install: %v", err)
			}
			if got, _ := os.ReadFile(f.wrapper); string(got) != string(wrapper) {
				t.Fatalf("the command at %s is %q after the rollback", f.wrapper, got)
			}
			if len(f.restarts) != 1 || f.restarts[0] != versionScript("v0.2.133") {
				t.Fatalf("restarts after the rollback = %q, want the previous daemon once", f.restarts)
			}
			for _, leftover := range []string{f.dataDir + ".failed"} {
				if _, err := os.Stat(leftover); !os.IsNotExist(err) {
					t.Fatalf("%s left behind: %v", leftover, err)
				}
			}
		})
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

func TestTheCommandIsFoundWhateverCharactersItsInstallFolderHas(t *testing.T) {
	for _, name := range []string{"plain", "with space", "agenthail[archive]", "cost$HOME", "it's", "tab\there", "line\nbreak"} {
		t.Run(name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), name)
			bin := filepath.Join(t.TempDir(), "bin")
			for _, dir := range []string{dataDir, bin} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dataDir, "agenthail"), []byte(versionScript("v0.2.133")), 0755); err != nil {
				t.Fatal(err)
			}
			script := `printf -v DATA_BINARY_SHELL '%q' "$1/agenthail"; printf '#!/usr/bin/env bash\n# agenthail-managed-wrapper-v1\nexec %s "$@"\n' "$DATA_BINARY_SHELL" > "$2/agenthail"`
			if output, err := exec.Command("/bin/bash", "-c", script, "bash", dataDir, bin).CombinedOutput(); err != nil {
				t.Fatalf("write the wrapper as install.sh does: %v %s", err, output)
			}
			if wrapper, _, _ := managedWrapper(dataDir, []string{t.TempDir(), bin}); wrapper != filepath.Join(bin, "agenthail") {
				t.Fatalf("found %q for an install in %q", wrapper, dataDir)
			}
			other := dataDir + "-other"
			if err := os.MkdirAll(other, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(other, "agenthail"), []byte(versionScript("v0.2.133")), 0755); err != nil {
				t.Fatal(err)
			}
			if wrapper, _, _ := managedWrapper(other, []string{bin}); wrapper != "" {
				t.Fatalf("another install's folder matched %q", wrapper)
			}
		})
	}
}

func TestTheCommandIsFoundWhenTheInstallIsReachedThroughASymlink(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real", "agenthail")
	if err := os.MkdirAll(real, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "agenthail"), []byte(versionScript("v0.2.133")), 0755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(filepath.Dir(real), linked); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `printf -v DATA_BINARY_SHELL '%q' "$1/agenthail"; printf '#!/usr/bin/env bash\n# agenthail-managed-wrapper-v1\nexec %s "$@"\n' "$DATA_BINARY_SHELL" > "$2/agenthail"`
	if output, err := exec.Command("/bin/bash", "-c", script, "bash", filepath.Join(linked, "agenthail"), bin).CombinedOutput(); err != nil {
		t.Fatalf("write the wrapper: %v %s", err, output)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	for _, dataDir := range []string{resolved, filepath.Join(linked, "agenthail")} {
		wrapper, _, named := managedWrapper(dataDir, []string{bin})
		if wrapper != filepath.Join(bin, "agenthail") {
			t.Fatalf("with the install named %q, the command written through the symlink was not found (got %q)", dataDir, wrapper)
		}
		if named != filepath.Join(linked, "agenthail") {
			t.Fatalf("the installer would be given %q, not the folder its command names (%q)", named, filepath.Join(linked, "agenthail"))
		}
	}
}

func TestShellWordReadsWhatBashQuotes(t *testing.T) {
	for _, path := range []string{"/plain/agenthail", "/with space/a", "/br[a]ck$et/a", "/it's/a", "/tab\tline\nend/a", "/bell\a/e\x1b/a", `/back\slash"quote/a`} {
		output, err := exec.Command("/bin/bash", "-c", `printf %q "$1"`, "bash", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := shellWord(string(output) + ` "$@"`); !ok || got != path {
			t.Errorf("bash quoted %q as %s; read back %q", path, output, got)
		}
	}
	if got, ok := shellWord(`"/old wrapper/agenthail" "$@"`); !ok || got != "/old wrapper/agenthail" {
		t.Errorf("a double-quoted target read back %q", got)
	}
}
