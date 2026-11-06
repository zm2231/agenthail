package testkit

import (
	"fmt"
	"os"
	"path/filepath"
)

// RunInOwnHome runs a test binary in a new HOME it removes afterwards, so no
// test touches the person's own Agenthail, Claude or Codex state. Go's caches
// stay where they were, or a test that runs the go command downloads every
// module again.
func RunInOwnHome(run func() int) int {
	if err := keepGoEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: keep the go command's caches: %v\n", err)
		return 1
	}
	home, err := os.MkdirTemp("", "agenthail-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: make the test binary's HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	for _, name := range []string{"HOME", "CFFIXED_USER_HOME"} {
		os.Setenv(name, home)
	}
	for _, name := range []string{"CODEX_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		os.Unsetenv(name)
	}
	return run()
}

func keepGoEnv() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	for name, value := range map[string]string{
		"GOPATH":  filepath.Join(home, "go"),
		"GOCACHE": filepath.Join(cache, "go-build"),
		"GOENV":   filepath.Join(config, "go", "env"),
	} {
		if os.Getenv(name) == "" {
			os.Setenv(name, value)
		}
	}
	return nil
}
