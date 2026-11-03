package surfaces

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/surface"
)

type promptHooks struct {
	mu    sync.Mutex
	roots map[string]promptHookState
}

type promptHookState struct {
	modified  time.Time
	size      int64
	installed bool
}

func (p *promptHooks) installed(root string) bool {
	if root == "" {
		return false
	}
	path := filepath.Join(root, "settings.json")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if known, ok := p.roots[root]; ok && known.modified.Equal(info.ModTime()) && known.size == info.Size() {
		return known.installed
	}
	state, err := claudehooks.StateAt(path, claudehooks.Prompt)
	installed := err == nil && state == claudehooks.Installed
	if p.roots == nil {
		p.roots = map[string]promptHookState{}
	}
	p.roots[root] = promptHookState{modified: info.ModTime(), size: info.Size(), installed: installed}
	return installed
}

// Stays fixed while prompts open and close: clients cache it, and waitingOn.answerable is per prompt.
func (c *Claude) answersPrompts(sess *surface.Session) bool {
	if sess.RuntimeKind != surface.RuntimeClaudeCLI {
		return false
	}
	if c.prompts.any(sess.ID) {
		return true
	}
	root := ""
	if sess.Transcript != "" {
		root = claudeTranscriptRoot(sess.Transcript)
	} else if found, err := c.conversationRoot(sess.ID); err == nil {
		root = found
	}
	return c.promptHooks.installed(root)
}
