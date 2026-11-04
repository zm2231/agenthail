package testkit

import (
	"fmt"
	"os"
)

// RunInOwnHome runs a test binary with HOME set to a new directory it removes
// afterwards, so no test writes the person's own ~/.agenthail and test
// binaries running side by side never share one.
func RunInOwnHome(run func() int) int {
	home, err := os.MkdirTemp("", "agenthail-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: make the test binary's HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: set the test binary's HOME: %v\n", err)
		return 1
	}
	return run()
}
