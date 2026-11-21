package main

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The CLI reaches the daemon only over its API, so the daemon's code stays out of every CLI build.
func TestTheCLIDoesNotImportTheDaemon(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		command := exec.Command("go", "list", "-deps", "github.com/zm2231/agenthail/internal/cli")
		command.Env = append(os.Environ(), "GOOS="+goos)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list for %s: %v\n%s", goos, err, output)
		}
		if slices.Contains(strings.Fields(string(output)), "github.com/zm2231/agenthail/internal/daemon") {
			t.Fatalf("internal/cli imports internal/daemon on %s", goos)
		}
	}
}
