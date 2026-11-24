package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/agentcard"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/projectresolve"
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

// codexTurnContext gives a Codex thread on the agenthail server Agenthail
// gave it what the session-context hook gives a Claude session: its card
// the first time, then one line whenever the card changes. The hook leaves
// such threads to the daemon, so the two never both tell one.
func (d *Daemon) codexTurnContext(ctx context.Context, session surface.Session) (string, func()) {
	card, err := d.cardSources().For(ctx, session)
	if err != nil || !card.EndsTurns {
		return "", nil
	}
	fingerprint := card.Fingerprint()
	given, found, err := d.Registry.ContextFingerprint(session.ID)
	if err != nil || found && given == fingerprint {
		return "", nil
	}
	text := agentcard.Render(card)
	if found {
		text = agentcard.RenderChange(card)
	}
	return text, func() {
		if _, err := d.Registry.ClaimContextChange(session.ID, fingerprint, time.Now()); err != nil {
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
