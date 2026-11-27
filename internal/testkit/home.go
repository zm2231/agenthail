package testkit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	homePrefix = "agenthail-test-home-"
	homeOwner  = ".agenthail-test-owner"
	staleAfter = 5 * time.Minute
)

// RunInOwnHome runs a test binary in a new HOME, with its TMPDIR inside, and
// removes it afterwards, so no test touches the person's own Agenthail, Claude
// or Codex state, and a killed binary's leftovers sit in one marked folder.
// It also drops the Claude configuration directory and every variable an agent
// session sets for its children: with them, a fixture writes into the real
// configuration directory and takes the enclosing session as its caller. Go's
// caches stay where they were, or a test that runs the go command downloads
// every module again.
func RunInOwnHome(run func() int) int {
	if err := keepGoEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: keep the go command's caches: %v\n", err)
		return 1
	}
	base := os.TempDir()
	removeDeadHomes(base)
	home, err := os.MkdirTemp(base, homePrefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: make the test binary's HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	if err := os.WriteFile(filepath.Join(home, homeOwner), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: mark the test binary's HOME: %v\n", err)
		return 1
	}
	tmp := filepath.Join(home, "t")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: make the test binary's TMPDIR: %v\n", err)
		return 1
	}
	for _, name := range []string{"HOME", "CFFIXED_USER_HOME"} {
		os.Setenv(name, home)
	}
	os.Setenv("TMPDIR", tmp)
	stopGitAt(tmp, base)
	for _, name := range []string{"CODEX_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		os.Unsetenv(name)
	}
	clearAgentEnv()
	stopGitAutoMaintenance()
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

// removeDeadHomes clears homes whose test binary was killed before its deferred
// removal ran. A home without an owner marker is never touched.
func removeDeadHomes(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), homePrefix) {
			continue
		}
		home := filepath.Join(dir, entry.Name())
		if owner, ok := deadOwner(filepath.Join(home, homeOwner)); ok && owner != os.Getpid() {
			os.RemoveAll(home)
		}
	}
}

func deadOwner(marker string) (int, bool) {
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) < staleAfter {
		return 0, false
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, false
	}
	return pid, errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
