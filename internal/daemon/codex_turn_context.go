package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessiontools"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func attachCodexTurnContext(d *Daemon, adapters []surface.Surface) {
	for _, adapter := range adapters {
		if attachable, ok := adapter.(interface {
			SetTurnContext(codexsurface.TurnContext)
		}); ok {
			attachable.SetTurnContext(d.codexTurnContext)
		}
	}
}

// Codex records a turn's context only when its key holds a new value, and
// forgets it on a turn that omits it; 0.171.0 and later put what it still
// holds back after compacting the thread. So every turn Agenthail starts
// carries the card, which Codex records once. An older app-server drops the
// card when it compacts the thread yet still holds it, so after a
// compaction the card moves to the other key, which Codex records again.
var codexContextKeys = [2]string{"agenthail", "agenthail_card"}

func otherCodexContextKey(key string) string {
	if key == codexContextKeys[0] {
		return codexContextKeys[1]
	}
	return codexContextKeys[0]
}

// codexTurnContext gives every turn of a Codex thread on the agenthail
// server Agenthail gave it the card the session-context hook gives a Claude
// session. The hook leaves such threads to the daemon, so the two never
// both tell one. The time recorded is the thread's last turn that carried
// the card, so a turn reads only the turns since for a compaction.
func (d *Daemon) codexTurnContext(ctx context.Context, session surface.Session, compactedSince func(time.Time) (bool, error)) (string, string, func()) {
	card, err := d.cardSources().For(ctx, session)
	if err != nil || !card.EndsTurns {
		return "", "", nil
	}
	given, found, err := d.Registry.ContextGiven(session.ID)
	if err != nil {
		return "", "", nil
	}
	key := given.Key
	switch {
	case !found || key == "":
		key = codexContextKeys[0]
	case compactedSince != nil:
		compacted, err := compactedSince(given.At)
		if err != nil {
			d.log.Printf("read whether %s compacted since its card: %s", session.ID, err)
		}
		if compacted {
			key = otherCodexContextKey(key)
		}
	}
	sent := registry.GivenContext{Fingerprint: card.Fingerprint(), Key: key, At: time.Now()}
	return key, agentcard.Render(card), func() {
		if err := d.Registry.RecordContextGiven(session.ID, sent); err != nil {
			d.log.Printf("record the Agenthail context given to %s: %s", session.ID, err)
		}
	}
}

func (d *Daemon) cardSources() agentcard.Sources {
	return agentcard.Sources{
		Registry: d.Registry,
		Project: func(ctx context.Context, cwd string) (*agentcard.Project, error) {
			found, err := projectresolve.ResolvePathProject(ctx, d.Registry, cwd)
			if err != nil || found == nil {
				return nil, err
			}
			return &agentcard.Project{ID: found.ID, Name: found.DisplayName, Root: found.RootPath}, nil
		},
		LeadRef: d.coordinator().Ref,
		PromptHearer: func(_ context.Context, session surface.Session) string {
			recipient, _ := d.noticeRecipient(session)
			return recipient.sessionID
		},
		MCP: func(_ context.Context, session surface.Session) agenttools.Access {
			return sessiontools.Of(d.Registry, session, func() (string, error) {
				if session.Cwd == "" {
					return "", nil
				}
				project, err := d.sessionProject(session)
				if err != nil || project == nil {
					return "", err
				}
				return project.ID, nil
			})
		},
	}
}
