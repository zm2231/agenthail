package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type launcherIntent struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}

// cmdLauncherExec is intentionally undocumented. It is the fixed, shell-safe
// target used by cmux's initial shell command; user data is read from a 0600
// intent file and never placed in that command string.
func (a *App) cmdLauncherExec(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("launcher-exec requires one intent path")
	}
	path := filepath.Clean(args[0])
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect launcher intent: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return errors.New("launcher intent must be a regular 0600 file")
	}
	data, err := os.ReadFile(path)
	_ = os.Remove(path)
	if err != nil {
		return fmt.Errorf("read launcher intent: %w", err)
	}
	var intent launcherIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return fmt.Errorf("parse launcher intent: %w", err)
	}
	if err := validateLauncherIntent(intent); err != nil {
		return err
	}
	cmd := exec.CommandContext(context.Background(), intent.Argv[0], intent.Argv[1:]...)
	cmd.Dir = intent.Cwd
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run launcher intent: %w", err)
	}
	return nil
}

func validateLauncherIntent(intent launcherIntent) error {
	if len(intent.Argv) == 0 || strings.TrimSpace(intent.Argv[0]) == "" {
		return errors.New("launcher intent has no executable")
	}
	if intent.Cwd == "" || !filepath.IsAbs(intent.Cwd) {
		return errors.New("launcher intent requires absolute cwd")
	}
	for _, arg := range intent.Argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return errors.New("launcher intent contains NUL")
		}
	}
	return nil
}
