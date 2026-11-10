package surfaces

import (
	"os"
	"path/filepath"
	"strings"
)

// CodexHome is where one Codex adapter keeps everything: Codex's own home and
// this daemon's state for its Codex locks. It is read once, so two adapters in
// one process can work on separate homes.
type CodexHome struct {
	Codex string
	State string
}

func CodexHomeFromEnv() CodexHome {
	userHome, _ := os.UserHomeDir()
	codex := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codex == "" {
		codex = filepath.Join(userHome, ".codex")
	}
	return CodexHome{Codex: codex, State: filepath.Join(userHome, ".agenthail")}
}

func (h CodexHome) sessionsDir() string { return filepath.Join(h.Codex, "sessions") }

func (h CodexHome) archivedDir() string { return filepath.Join(h.Codex, "archived_sessions") }

func (h CodexHome) moveDir(threadID, moveID string) string {
	return filepath.Join(h.Codex, claudeMovedDir, threadID, moveID)
}

func (h CodexHome) socketPath() string {
	return filepath.Join(h.Codex, "app-server-control", "app-server-control.sock")
}

func (h CodexHome) writeLockPath() string { return filepath.Join(h.State, "codex-write.lock") }

func (h CodexHome) threadLockPath(threadID string) string {
	return filepath.Join(h.State, "codex-threads", threadID+".lock")
}

func (h CodexHome) daemonPackageBinaries() []string {
	packages := filepath.Join(h.Codex, "packages")
	return []string{
		filepath.Join(packages, "app-server-daemon", "current", "bin", "codex"),
		filepath.Join(packages, "standalone", "current", "codex"),
	}
}

func (h CodexHome) ChildEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "CODEX_HOME=") {
			out = append(out, entry)
		}
	}
	return append(out, "CODEX_HOME="+h.Codex)
}
