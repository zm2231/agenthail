package surfaces

import (
	"fmt"
	"os/user"
	"path/filepath"
	"runtime"
)

const codexDesktopOptIn = "set AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT to reach Codex Desktop from this Codex home"

func (c *Codex) DesktopScope() error {
	if c.desktopURL != "" {
		return nil
	}
	if codexDesktopRunsHere {
		return fmt.Errorf("Codex Desktop is not used from this Codex home (%s); %s", codexHome(), codexDesktopOptIn)
	}
	return fmt.Errorf("Codex Desktop does not run on %s; set AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT to reach one", runtime.GOOS)
}

// From the user database, not HOME, so a scratch HOME cannot claim the account's Desktop.
var codexAccountHome = func() string {
	account, err := user.Current()
	if err != nil {
		return ""
	}
	return account.HomeDir
}

func codexHomeIsAccountDefault() bool {
	account := codexAccountHome()
	if account == "" {
		return false
	}
	return sameCodexPath(codexHome(), filepath.Join(account, ".codex"))
}

func sameCodexPath(a, b string) bool {
	return resolvedCodexPath(a) == resolvedCodexPath(b)
}

func resolvedCodexPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	dir, base := filepath.Split(path)
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(dir)); err == nil {
		return filepath.Join(resolved, base)
	}
	return path
}
