package surfaces

import (
	"os/user"
	"path/filepath"
)

const codexDesktopOptIn = "set AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT to reach Codex Desktop from this Codex home"

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
