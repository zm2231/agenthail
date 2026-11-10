package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func recordedWait(r *registry.Registry, sessionID string) (*surface.Waiting, bool, error) {
	open, err := r.OpenPermissionAsks(sessionID)
	if err != nil || len(open) == 0 {
		return nil, false, err
	}
	waiting, err := r.SessionWaiting(sessionID)
	return waiting, !open[0].AnnouncedAt.IsZero(), err
}

func recordWait(t *testing.T, r *registry.Registry, sessionID string, waiting *surface.Waiting) registry.WaitSync {
	t.Helper()
	sync, err := r.SyncWaits(sessionID, waitsOf(waiting), "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return sync
}

func askOfRequest(r *registry.Registry, sessionID, ref string) (registry.Ask, bool) {
	asks, err := r.SessionAsks(sessionID)
	if err != nil {
		return registry.Ask{}, false
	}
	for i := len(asks) - 1; i >= 0; i-- {
		if asks[i].Origin == registry.AskOriginPermission && asks[i].Ref == ref {
			return asks[i], true
		}
	}
	return registry.Ask{}, false
}

func askRef(r *registry.Registry, askID string) string {
	ask, _, _ := r.Ask(askID)
	return ask.Ref
}

func askRefs(r *registry.Registry, askIDs []string) []string {
	refs := make([]string, 0, len(askIDs))
	for _, id := range askIDs {
		refs = append(refs, askRef(r, id))
	}
	return refs
}

func askIDOfRequest(t *testing.T, r *registry.Registry, sessionID, ref string) string {
	t.Helper()
	ask, found := askOfRequest(r, sessionID, ref)
	if !found {
		t.Fatalf("no ask for request %s of %s", ref, sessionID)
	}
	return ask.ID
}

func answerRequest(ctx context.Context, d *Daemon, adapter surface.Surface, session *surface.Session, ref string, response surface.Response) (actionResult, error) {
	ask, found := askOfRequest(d.Registry, session.ID, ref)
	if !found {
		if _, err := d.observeTurn(ctx, adapter, session); err != nil {
			return actionResult{}, err
		}
		if ask, found = askOfRequest(d.Registry, session.ID, ref); !found {
			return actionResult{}, staleRequest()
		}
	}
	return d.answerAsk(ctx, adapter, session, ask.ID, response, principal{ID: localDashboardPrincipal, Kind: principalPerson}, "")
}
