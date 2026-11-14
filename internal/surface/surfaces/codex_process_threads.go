package surfaces

import (
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/peerproc"
)

// ProcessThreads names the threads the Codex process pid has loaded: Codex
// holds CODEX_HOME/thread-writer-locks/<thread>.lock open for every thread it
// loads, in the CLI and in the app-server alike.
func (c *Codex) ProcessThreads(pid int) []string {
	dir, err := filepath.EvalSymlinks(filepath.Join(c.home.Codex, "thread-writer-locks"))
	if err != nil {
		return nil
	}
	files, err := peerproc.OpenFiles(pid)
	if err != nil {
		return nil
	}
	var threads []string
	for _, file := range files {
		if filepath.Dir(file) != dir {
			continue
		}
		if thread, ok := strings.CutSuffix(filepath.Base(file), ".lock"); ok && thread != "" {
			threads = append(threads, thread)
		}
	}
	return threads
}
