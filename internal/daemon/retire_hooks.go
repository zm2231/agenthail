package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// retireStrayHooks removes the agenthail hooks an earlier install wrote that
// this binary no longer writes, such as the Stop hook, from every Claude
// config root and from Codex, so an update clears them without hooks install.
func (d *Daemon) retireStrayHooks(ctx context.Context, home string, codex surfaces.CodexHome) {
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		d.log.Printf("hooks: list Claude config roots: %s", err)
	}
	for _, root := range roots {
		path := filepath.Join(root, "settings.json")
		if changed, err := claudehooks.RetireAt(path); err != nil {
			d.log.Printf("hooks: remove retired agenthail hooks from %s: %s", path, err)
		} else if changed {
			d.log.Printf("hooks: removed retired agenthail hooks from %s", path)
		}
	}
	if codex.Codex == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if changed, err := surfaces.RetireCodexHooks(ctx, codex); err != nil {
		d.log.Printf("hooks: remove retired agenthail hooks from Codex: %s", err)
	} else if changed {
		d.log.Printf("hooks: removed retired agenthail hooks from Codex in %s", codex.Codex)
	}
}

func (d *Daemon) retireStrayHooksAtStart(home string) {
	d.background.start(func(ctx context.Context) { d.retireStrayHooks(ctx, home, surfaces.CodexHomeFromEnv()) })
}
