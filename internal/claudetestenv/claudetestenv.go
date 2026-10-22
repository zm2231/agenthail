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
	for _, name := range sessionVariables() {
		t.Setenv(name, "")
	}
}

// Clear removes the same variables for the whole test binary; a package's
// TestMain calls it before running its tests.
func Clear() {
	for _, name := range sessionVariables() {
		os.Unsetenv(name)
	}
}

func sessionVariables() []string {
	var names []string
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if name == "CLAUDE_CONFIG_DIR" || name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			names = append(names, name)
		}
	}
	return names
}
