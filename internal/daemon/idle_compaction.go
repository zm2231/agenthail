package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// A conversation idle this long is compacted before the hour its prompt
// cache lasts runs out, so the compaction reads the cache instead of writing
// it again.
const (
	idleCompactionEvery  = time.Minute
	idleCompactionAfter  = 50 * time.Minute
	idleCompactionBefore = time.Hour
)

type idleTranscript struct {
	size    int64
	modTime time.Time
	state   surface.IdleCompactionState
}

func (d *Daemon) runIdleCompaction(ctx context.Context) {
	ticker := time.NewTicker(idleCompactionEvery)
	defer ticker.Stop()
	read := map[string]idleTranscript{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.compactIdle(ctx, now, read)
		}
	}
}

// compactIdle queues a compact for each Claude session of this host whose
// transcript shows a large request, idle in the hour its cache lasts. A
// session that shows one but is held, such as one still marked busy or under
// a root at its usage limit, has the first hold recorded once per request.
// A usage limit anywhere under a config root holds the root's sessions, since
// the compact would be refused too; being near the limit does not.
func (d *Daemon) compactIdle(ctx context.Context, now time.Time, read map[string]idleTranscript) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	sessions, err := d.Registry.ListSessions(0)
	if err != nil {
		d.log.Printf("read sessions for idle compaction: %s", err)
		return
	}
	refused := map[string]bool{}
	for _, session := range sessions {
		if session.Surface == surface.KindClaude && session.UsageLimit != nil && (session.UsageLimit.ResetsAt.IsZero() || session.UsageLimit.ResetsAt.After(now)) {
			refused[claudeTranscriptRoot(session.Transcript)] = true
		}
	}
	settings := map[string]surface.IdleCompaction{}
	seen := map[string]bool{}
	for i := range sessions {
		if ctx.Err() != nil {
			return
		}
		session := &sessions[i]
		if session.Surface != surface.KindClaude || session.Transcript == "" {
			continue
		}
		seen[session.Transcript] = true
		if session.Ended {
			continue
		}
		info, err := os.Stat(session.Transcript)
		if err != nil || now.Sub(info.ModTime()) >= idleCompactionBefore {
			continue
		}
		root := claudeTranscriptRoot(session.Transcript)
		rootSettings, ok := settings[root]
		if !ok {
			if rootSettings, err = surface.ReadIdleCompaction(home, root); err != nil {
				d.log.Printf("read idle compaction setting for %s: %s", root, err)
				continue
			}
			settings[root] = rootSettings
		}
		adapter, err := d.adapters().For(session)
		if err != nil {
			continue
		}
		reader, ok := adapter.(surface.IdleCompactionReader)
		if !ok {
			continue
		}
		known, ok := read[session.Transcript]
		if !ok || known.size != info.Size() || !known.modTime.Equal(info.ModTime()) {
			state, err := reader.IdleCompactionState(ctx, session)
			if err != nil {
				continue
			}
			known = idleTranscript{size: info.Size(), modTime: info.ModTime(), state: state}
			read[session.Transcript] = known
		}
		state := known.state
		idle := now.Sub(state.LastRequestAt)
		if state.LastRequestAt.IsZero() || idle < idleCompactionAfter || idle >= idleCompactionBefore || !state.OneHourCache || state.CompactedSince || state.ContextTokens < rootSettings.MinContextTokens {
			continue
		}
		hold := idleCompactionHold(session, rootSettings, refused[root], surface.EffectiveCapabilities(adapter, session).Compact)
		if hold == "" {
			outcome, err := d.Registry.QueueIdleCompaction(session.ID, state.LastRequestAt)
			if err != nil {
				d.log.Printf("queue idle compaction for %s: %s", d.resolveDisplay(session.ID), err)
				continue
			}
			switch outcome {
			case registry.IdleCompactionQueued:
				d.log.Printf("queued idle compaction for %s at %d tokens, idle %s", d.resolveDisplay(session.ID), state.ContextTokens, idle.Round(time.Minute))
				continue
			case registry.IdleCompactionAttempted:
				continue
			}
			hold = "a message or command is queued or in flight"
		}
		d.idleHolds.record(IdleCompactionHold{SessionID: session.ID, Root: root, RequestAt: state.LastRequestAt, ContextTokens: state.ContextTokens, Reason: hold, At: now}, func(held IdleCompactionHold) {
			d.log.Printf("idle compaction held for %s at %d tokens, idle %s: %s", d.resolveDisplay(held.SessionID), held.ContextTokens, idle.Round(time.Minute), held.Reason)
		})
	}
	for path := range read {
		if !seen[path] {
			delete(read, path)
		}
	}
}

func claudeTranscriptRoot(transcript string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(transcript)))
}
