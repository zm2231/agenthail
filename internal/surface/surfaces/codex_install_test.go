package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func installDaemonPackageCodex(t *testing.T, codexHome, script string) string {
	t.Helper()
	path := filepath.Join(codexHome, "packages", "app-server-daemon", "current", "bin", "codex")
	writeExecutable(t, path, script)
	return path
}

func installStandaloneCodex(t *testing.T, codexHome, script string) string {
	t.Helper()
	path := filepath.Join(codexHome, "packages", "standalone", "current", "codex")
	writeExecutable(t, path, script)
	return path
}

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func isolateCodexInstalls(t *testing.T, desktop bool) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-path"))
	previousDesktop := CodexDesktopExecutables
	CodexDesktopExecutables = []string{filepath.Join(root, "Codex.app", "Contents", "MacOS", "Codex")}
	if desktop {
		writeExecutable(t, CodexDesktopExecutables[0], "#!/bin/sh\n")
	}
	t.Cleanup(func() { CodexDesktopExecutables = previousDesktop })
}

func TestCodexDaemonCLIPrefersTheDaemonPackageThenPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	isolateCodexInstalls(t, true)
	if _, err := CodexDaemonCLI(); !errors.Is(err, ErrCodexMissing) || !strings.Contains(err.Error(), CodexInstallCommand) {
		t.Fatalf("err=%v, want the missing CLI with the install command", err)
	}
	bundleDir := filepath.Join(t.TempDir(), "ChatGPT.app", "Contents", "Resources")
	writeExecutable(t, filepath.Join(bundleDir, "codex"), "#!/bin/sh\n")
	npmDir := t.TempDir()
	npm := filepath.Join(npmDir, "codex")
	writeExecutable(t, npm, "#!/bin/sh\n")
	t.Setenv("PATH", bundleDir+string(os.PathListSeparator)+npmDir)
	if path, err := CodexDaemonCLI(); err != nil || path != npm {
		t.Fatalf("path=%q err=%v, want the PATH CLI and never the Desktop bundle", path, err)
	}
	standalone := installStandaloneCodex(t, home, "#!/bin/sh\n")
	if path, _ := CodexDaemonCLI(); path != standalone {
		t.Fatalf("path=%q, want the legacy daemon package", path)
	}
	packaged := installDaemonPackageCodex(t, home, "#!/bin/sh\n")
	install := DetectCodexInstallation()
	if install.Daemon != packaged || install.Desktop != CodexDesktopExecutables[0] {
		t.Fatalf("install=%+v", install)
	}
}

func TestCodexRuntimeStatusNamesWhatEachInstallStateIsMissing(t *testing.T) {
	stoppedDaemon := "#!/bin/sh\nexit 1\n"
	for _, test := range []struct {
		name        string
		bridge      bool
		desktop     bool
		cli         string
		problem     surface.RuntimeProblem
		reachable   bool
		remediation string
		notes       []surface.RuntimeProblem
	}{
		{name: "bridge up without a daemon CLI", bridge: true, reachable: true},
		{name: "no Codex at all", problem: surface.RuntimeCodexMissing, remediation: CodexInstallCommand},
		{name: "Desktop closed, no daemon CLI", desktop: true, problem: surface.RuntimeBridgeUnavailable, remediation: "agenthail launch codex"},
		{name: "daemon stopped, no Desktop", cli: stoppedDaemon, problem: surface.RuntimeStopped, remediation: "starts it"},
		{name: "daemon stopped, Desktop closed", cli: stoppedDaemon, desktop: true, problem: surface.RuntimeBridgeUnavailable, remediation: "agenthail launch codex", notes: []surface.RuntimeProblem{surface.RuntimeStopped}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			isolateCodexInstalls(t, test.desktop)
			if test.cli != "" {
				npmDir := t.TempDir()
				writeExecutable(t, filepath.Join(npmDir, "codex"), test.cli)
				t.Setenv("PATH", npmDir)
			}
			codex := isolatedManagedRuntime(t)
			if test.bridge {
				codex = NewCodex(startRendererDesktopBridge(t))
				codex.managed = true
			}
			status := codex.RuntimeStatus(context.Background())
			if status.Problem != test.problem || status.Reachable != test.reachable || !strings.Contains(status.Remediation, test.remediation) {
				t.Fatalf("status=%+v", status)
			}
			if len(status.Notes) != len(test.notes) {
				t.Fatalf("notes=%+v, want %v", status.Notes, test.notes)
			}
			for index, note := range status.Notes {
				if note.Problem != test.notes[index] || note.Remediation == "" {
					t.Fatalf("note=%+v", note)
				}
			}
		})
	}
}

func TestDetectCodexInstallationFindsPerUserDesktop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", t.TempDir())
	isolateCodexInstalls(t, false)
	perUser := []string{}
	for _, path := range codexDesktopExecutables() {
		if strings.HasPrefix(path, home) {
			perUser = append(perUser, path)
		}
	}
	CodexDesktopExecutables = perUser
	desktop := filepath.Join(home, "Applications", "Codex.app", "Contents", "MacOS", "Codex")
	writeExecutable(t, desktop, "#!/bin/sh\n")
	if got := DetectCodexInstallation().Desktop; got != desktop {
		t.Fatalf("desktop=%q, want the ~/Applications install", got)
	}
}
