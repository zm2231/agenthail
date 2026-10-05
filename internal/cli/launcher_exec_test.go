package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherExecConsumesIntentAndPreservesArgv(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "agent")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LAUNCHER_EXEC_ARGS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAUNCHER_EXEC_ARGS", argsPath)
	intentPath := filepath.Join(dir, "intent.json")
	intent := launcherIntent{Argv: []string{script, `$(touch /tmp/nope); "quoted"`}, Cwd: dir}
	data, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intentPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&App{}).Run([]string{"launcher-exec", intentPath}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(intentPath); !os.IsNotExist(err) {
		t.Fatalf("intent was not consumed: %v", err)
	}
	got, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != `$(touch /tmp/nope); "quoted"` {
		t.Fatalf("argv = %q", got)
	}
}

func TestLauncherExecRejectsUnprotectedIntent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intent.json")
	if err := os.WriteFile(path, []byte(`{"argv":["/bin/true"],"cwd":"/"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := (&App{}).Run([]string{"launcher-exec", path}); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("error = %v, want 0600 rejection", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rejected intent should remain for diagnosis: %v", err)
	}
}
