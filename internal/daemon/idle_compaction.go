package daemon

import (
	"context"
	"fmt"
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
	read    surface.IdleCompactionRead
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
		case <-d.idleWake:
			d.compactIdle(ctx, time.Now(), read)
		}
	}
}

// wakeIdleCompaction asks for an idle compaction pass now, as when a retiring
// Claude host reports a session's state, so a session it need not keep is
// released at once.
func (d *Daemon) wakeIdleCompaction() {
	select {
	case d.idleWake <- struct{}{}:
	default:
	}
}

// idleLook is what a session's transcript says about compacting it while
// idle. A look that is not ok has no window to compact: why says what stands
// in the way.
type idleLook struct {
	ok       bool
	why      string
	state    surface.IdleCompactionState
	root     string
	settings surface.IdleCompaction
	adapter  surface.Surface
}

// compactIdle queues a compact for each Claude session of this host whose
// transcript shows a large request, idle in the hour its cache lasts. A
// session that shows one but is held, such as one still marked busy, ended,
// or under a root at its usage limit, has the first hold recorded once per
// request. A usage limit anywhere under a config root holds the root's
// sessions, since the compact would be refused too; being near the limit does
// not. A session a retiring Claude host runs is kept there while it could
// still be compacted and released otherwise.
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
	retiring := d.retiringClaudeSessions(ctx)
	settings := map[string]surface.IdleCompaction{}
	seen, listed, decided := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range sessions {
		if ctx.Err() != nil {
			return
		}
		session := &sessions[i]
		if session.Surface != surface.KindClaude || session.Transcript == "" {
			continue
		}
		seen[session.Transcript] = true
		listed[session.ID] = true
		look := d.lookIdle(ctx, home, session, now, read, settings)
		if host, ok := retiring[session.ID]; ok {
			decided[session.ID] = true
			d.decideRetiring(ctx, host, session, look, refused[look.root])
		}
		if !look.ok {
			continue
		}
		state := look.state
		idle := now.Sub(state.LastRequestAt)
		if idle < idleCompactionAfter || !idleCompactionWorthy(state, look.settings) {
			continue
		}
		hold := "the session has ended"
		if !session.Ended {
			hold = idleCompactionHold(session, look.settings, refused[look.root], surface.EffectiveCapabilities(look.adapter, session).Compact)
		}
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
		d.idleHolds.record(IdleCompactionHold{SessionID: session.ID, Root: look.root, RequestAt: state.LastRequestAt, ContextTokens: state.ContextTokens, Reason: hold, At: now}, func(held IdleCompactionHold) {
			d.log.Printf("idle compaction held for %s at %d tokens, idle %s: %s", d.resolveDisplay(held.SessionID), held.ContextTokens, idle.Round(time.Minute), held.Reason)
		})
	}
	for id, host := range retiring {
		if !decided[id] {
			d.decideRetiring(ctx, host, &surface.Session{ID: id}, idleLook{why: "Agenthail has no transcript for it"}, false)
		}
	}
	for path := range read {
		if !seen[path] {
			delete(read, path)
		}
	}
	d.idleHolds.forgetExcept(listed)
	d.retiringDecisions.forgetExcept(retiring)
}

// lookIdle reads a session's transcript, continuing the last read of the same
// file, and its root's idle compaction setting. A look is ok while the newest
// request is under an hour old.
func (d *Daemon) lookIdle(ctx context.Context, home string, session *surface.Session, now time.Time, read map[string]idleTranscript, settings map[string]surface.IdleCompaction) idleLook {
	look := idleLook{root: claudeTranscriptRoot(session.Transcript)}
	info, err := os.Stat(session.Transcript)
	if err != nil {
		look.why = "its transcript cannot be read"
		return look
	}
	if now.Sub(info.ModTime()) >= idleCompactionBefore {
		look.why = "its prompt cache has expired"
		return look
	}
	rootSettings, ok := settings[look.root]
	if !ok {
		if rootSettings, err = surface.ReadIdleCompaction(home, look.root); err != nil {
			d.log.Printf("read idle compaction setting for %s: %s", look.root, err)
			look.why = "its root's idle compaction setting cannot be read"
			return look
		}
		settings[look.root] = rootSettings
	}
	look.settings = rootSettings
	adapter, err := d.adapters().For(session)
	if err != nil {
		look.why = "no adapter runs it"
		return look
	}
	look.adapter = adapter
	reader, ok := adapter.(surface.IdleCompactionReader)
	if !ok {
		look.why = "its transcript cannot be read"
		return look
	}
	known, ok := read[session.Transcript]
	if !ok || known.size != info.Size() || !known.modTime.Equal(info.ModTime()) {
		next, err := reader.IdleCompactionState(ctx, session, known.read)
		if err != nil {
			delete(read, session.Transcript)
			look.why = "its transcript cannot be read"
			return look
		}
		known = idleTranscript{size: info.Size(), modTime: info.ModTime(), read: next}
		read[session.Transcript] = known
	}
	look.state = known.read.State
	switch {
	case look.state.LastRequestAt.IsZero():
		look.why = "its transcript shows no request"
	case now.Sub(look.state.LastRequestAt) >= idleCompactionBefore:
		look.why = "its prompt cache has expired"
	default:
		look.ok = true
	}
	return look
}

// idleCompactionWorthy is a request whose context compaction would save
// writing again: large enough, cached for an hour and not compacted since.
func idleCompactionWorthy(state surface.IdleCompactionState, settings surface.IdleCompaction) bool {
	return state.OneHourCache && !state.CompactedSince && state.ContextTokens >= settings.MinContextTokens
}

func claudeTranscriptRoot(transcript string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(transcript)))
}

// retiringKeep reports whether a retiring host should keep the session for
// its idle compaction, and otherwise why it need not.
func retiringKeep(session *surface.Session, look idleLook, refused bool) (keep bool, release string) {
	state := look.state
	switch {
	case !look.ok:
		return false, look.why
	case !look.settings.Enabled:
		return false, "idle compaction is off for this root"
	case state.CompactedSince:
		return false, "it has compacted since its last request"
	case !state.OneHourCache:
		return false, "its last cache write lasts five minutes"
	case state.ContextTokens < look.settings.MinContextTokens:
		return false, fmt.Sprintf("its %d tokens are under the %d-token threshold", state.ContextTokens, look.settings.MinContextTokens)
	case refused:
		return false, "the account under this root is at a usage limit"
	case !surface.EffectiveCapabilities(look.adapter, session).Compact:
		return false, "it cannot compact"
	}
	return true, ""
}
