package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheDaemonNeverBoundsRegistryWritesToADeadline(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "WriteWithin(") {
			t.Errorf("%s binds registry writes to a deadline; the daemon keeps the full busy timeout", file)
		}
	}
}
