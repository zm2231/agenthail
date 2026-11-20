package testkit

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGitInTestsLeavesNoMaintenanceRunning(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	command := exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x")
	command.Env = append(command.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TRACE=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "maintenance run") || strings.Contains(string(output), "gc --auto") {
		t.Fatalf("a commit in a test started maintenance:\n%s", output)
	}
}
