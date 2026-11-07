package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/uiconf"
)

func TestDoctorReadsUIConfProblemsWithoutTouchingTheFile(t *testing.T) {
	dir := t.TempDir()
	app := &App{UIDir: dir}
	if problems, err := app.uiConfProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("no ui.conf: %v %v", problems, err)
	}
	path := filepath.Join(dir, uiconf.FileName)
	if err := os.WriteFile(path, []byte("glass.sidebar.opacity = 0.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	problems, err := app.uiConfProblems()
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0].String(), "ui.conf:1: glass.sidebar.opacity was removed; use glass.opacity") {
		t.Fatalf("problems %v %v", problems, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("doctor created a repository in the ui folder: %v", err)
	}
}
