// Package claudetestenv keeps tests that run Claude fixtures from reading the
// Claude session the tests themselves run in.
package claudetestenv

import (
	"os"
	"strings"
	"testing"
)

// Isolate clears the Claude configuration directory and every variable a
// Claude session sets for its children, for the rest of the test. With them,
// a fixture reads and writes the real configuration directory and takes the
// enclosing session as its caller.
func Isolate(t testing.TB) {
	t.Helper()
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name == "CLAUDE_CONFIG_DIR" || name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			t.Setenv(name, "")
		}
	}
}
