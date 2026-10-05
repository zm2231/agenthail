package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

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

func isolateCodexInstalls(t *testing.T, desktop bool) (bundled string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "empty-path"))
	bundled = filepath.Join(root, "ChatGPT.app", "Contents", "Resources", "codex")
	previousBundled := codexBundledCLIs
	codexBundledCLIs = func() []string { return []string{bundled} }
	previousDesktop := CodexDesktopExecutables
	CodexDesktopExecutables = []string{filepath.Join(root, "Codex.app", "Contents", "MacOS", "Codex")}
	if desktop {
		writeExecutable(t, CodexDesktopExecutables[0], "#!/bin/sh\n")
	}
	t.Cleanup(func() {
		codexBundledCLIs = previousBundled
		CodexDesktopExecutables = previousDesktop
	})
	return bundled
}

func TestDetectCodexInstallationFindsEveryCodexOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	bundled := isolateCodexInstalls(t, true)
	standalone := installStandaloneCodex(t, home, "#!/bin/sh\n")
	linkDir, npmDir := t.TempDir(), t.TempDir()
	if err := os.Symlink(standalone, filepath.Join(linkDir, "codex")); err != nil {
		t.Fatal(err)
	}
	npm := filepath.Join(npmDir, "codex")
	writeExecutable(t, npm, "#!/bin/sh\n")
	writeExecutable(t, bundled, "#!/bin/sh\n")
	t.Setenv("PATH", linkDir+string(os.PathListSeparator)+npmDir)

	install := DetectCodexInstallation()
	if install.Standalone != standalone || install.Desktop != CodexDesktopExecutables[0] {
		t.Fatalf("install=%+v", install)
	}
	if len(install.Other) != 2 || install.Other[0] != npm || install.Other[1] != bundled {
		t.Fatalf("other=%v, want the npm CLI and the ChatGPT.app CLI without the standalone symlink", install.Other)
	}
}

func TestManagedCodexBinaryUsesOnlyTheStandaloneRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	isolateCodexInstalls(t, false)
	npmDir := t.TempDir()
	writeExecutable(t, filepath.Join(npmDir, "codex"), "#!/bin/sh\n")
	t.Setenv("PATH", npmDir)
	if _, err := ManagedCodexBinary(); err == nil || !strings.Contains(err.Error(), filepath.Join(npmDir, "codex")) || !strings.Contains(err.Error(), CodexStandaloneInstallCommand) {
		t.Fatalf("err=%v, want the missing standalone runtime naming the npm CLI and the install command", err)
	}
	standalone := installStandaloneCodex(t, home, "#!/bin/sh\n")
	if path, err := ManagedCodexBinary(); err != nil || path != standalone {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestCodexRuntimeStatusNamesWhatEachInstallStateIsMissing(t *testing.T) {
	stoppedDaemon := "#!/bin/sh\nexit 1\n"
	for _, test := range []struct {
		name        string
		bridge      bool
		desktop     bool
		standalone  string
		problem     surface.RuntimeProblem
		reachable   bool
		remediation string
		notes       []surface.RuntimeProblem
	}{
		{name: "bridge up without standalone", bridge: true, reachable: true, notes: []surface.RuntimeProblem{surface.RuntimeStandaloneMissing}},
		{name: "npm only, no Desktop", problem: surface.RuntimeStandaloneMissing, remediation: CodexStandaloneInstallCommand},
		{name: "Desktop closed, no standalone", desktop: true, problem: surface.RuntimeBridgeUnavailable, remediation: "agenthail launch codex", notes: []surface.RuntimeProblem{surface.RuntimeStandaloneMissing}},
		{name: "standalone stopped, no Desktop", standalone: stoppedDaemon, problem: surface.RuntimeStopped, remediation: "agenthail codex"},
		{name: "standalone stopped, Desktop closed", standalone: stoppedDaemon, desktop: true, problem: surface.RuntimeBridgeUnavailable, remediation: "agenthail launch codex", notes: []surface.RuntimeProblem{surface.RuntimeStopped}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("CODEX_HOME", home)
			isolateCodexInstalls(t, test.desktop)
			npmDir := t.TempDir()
			writeExecutable(t, filepath.Join(npmDir, "codex"), "#!/bin/sh\nexit 1\n")
			t.Setenv("PATH", npmDir)
			if test.standalone != "" {
				installStandaloneCodex(t, home, test.standalone)
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
				if note.Problem == surface.RuntimeStandaloneMissing && (!strings.Contains(note.Message, filepath.Join(npmDir, "codex")) || !strings.Contains(note.Remediation, CodexStandaloneInstallCommand)) {
					t.Fatalf("standalone note does not name the npm CLI and install command: %+v", note)
				}
			}
			if test.problem == surface.RuntimeStandaloneMissing && !strings.Contains(status.Detail, filepath.Join(npmDir, "codex")) {
				t.Fatalf("detail=%q does not name the npm CLI", status.Detail)
			}
		})
	}
}
