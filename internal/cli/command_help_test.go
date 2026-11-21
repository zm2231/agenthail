package cli

import (
	"strings"
	"testing"
)

func TestACommandsHelpPrintsThatCommandsUsage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	for _, args := range [][]string{{"send", "--help"}, {"send", "-h"}, {"help", "send"}} {
		out, err := captureStdout(t, func() error { return app.Run(args) })
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out, "  send <target>") || !strings.Contains(out, "--no-queue") || strings.Contains(out, "Session commands:") {
			t.Fatalf("%v printed:\n%s\nwant only send's usage and its flags", args, out)
		}
	}
}

func TestAnUnknownFlagNamesTheFlagsTheCommandTakes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := (&App{}).Run([]string{"reply", "@x", "--bogus"})
	if err == nil || !strings.Contains(err.Error(), "--bogus") || !strings.Contains(err.Error(), "--max-bytes <value>") || !strings.Contains(err.Error(), "--report") {
		t.Fatalf("err=%v, want the unknown flag and the flags reply takes", err)
	}
}
