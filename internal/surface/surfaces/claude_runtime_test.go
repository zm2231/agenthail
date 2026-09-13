package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeNativeInstallWorksWithoutShellPath(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$1" = agents ]; then
  printf '%s\n' '[{"id":"native-agent","sessionId":"native-session","kind":"background"}]'
else
  IFS= read -r request
  printf '%s\n' '{"type":"control_response","response":{"request_id":"agenthail-model-catalog","response":{"models":[{"value":"native-model","displayName":"Native model"}]}}}'
  /bin/sleep 30
fi
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	adapter := NewClaude("", home)
	models, err := adapter.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "native-model" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	agents, err := adapter.backgroundSessions(context.Background())
	if err != nil || len(agents) != 1 || agents[0].SessionID != "native-session" {
		t.Fatalf("agents=%+v err=%v", agents, err)
	}
}

func TestClaudeExecutableSelectionPreservesExplicitChoice(t *testing.T) {
	home := t.TempDir()
	native := filepath.Join(home, ".local", "bin", "claude")
	pathBinary := filepath.Join(t.TempDir(), "claude")
	custom := filepath.Join(t.TempDir(), "custom claude")
	for _, binary := range []string{native, pathBinary, custom} {
		if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(pathBinary))
	t.Setenv("AGENTHAIL_CLAUDE_BIN", custom)
	if got, err := claudeBinary(home); err != nil || got != custom {
		t.Fatalf("explicit binary=%q err=%v", got, err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", filepath.Join(home, "missing"))
	if got, err := claudeBinary(home); err == nil || got != "" {
		t.Fatalf("invalid explicit binary used another install: %q err=%v", got, err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	if got, err := claudeBinary(home); err != nil || got != pathBinary {
		t.Fatalf("PATH binary=%q err=%v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := claudeBinary(home); err != nil || got != native {
		t.Fatalf("native binary=%q err=%v", got, err)
	}
	if err := os.Chmod(native, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := claudeBinary(home); err == nil || got != "" {
		t.Fatalf("non-executable native install accepted: %q err=%v", got, err)
	}
}
