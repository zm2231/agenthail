package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func leadQueue(t *testing.T, r *registry.Registry) []string {
	t.Helper()
	rows, err := r.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	for _, row := range rows {
		if row.SessionID == "lead" {
			messages = append(messages, row.Message)
		}
	}
	return messages
}

func stoppedTurnFixture(t *testing.T) gateFixture {
	t.Helper()
	f := gateFixtureIn(t, registry.OriginAgent, surface.RuntimeClaudeHost, false)
	f.project.session(t, f.r, "lead")
	makeCoordinator(t, f.r, f.project, "lead")
	return f
}

func (f gateFixture) stopTurn(t *testing.T, turn string, stop *surface.TurnStop, limit *surface.UsageLimit) {
	t.Helper()
	f.fake.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: turn}
	f.d.observeSession(t.Context(), f.fake, &f.session)
	f.fake.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turn, Reply: &surface.ReplyResult{Done: true, Error: "failed"}, TurnStop: stop, UsageLimit: limit}
	f.d.observeSession(t.Context(), f.fake, &f.session)
}

func TestTheLeadIsToldOnceWhenTheSafeguardsRefuseASession(t *testing.T) {
	f := stoppedTurnFixture(t)
	stop := &surface.TurnStop{TurnID: "turn-1", At: time.Now().Add(-time.Minute), Reason: "Claude API error: flagged", Refused: true}
	f.stopTurn(t, "turn-1", stop, nil)
	f.stopTurn(t, "turn-1", stop, nil)
	queued := leadQueue(t, f.r)
	if len(queued) != 1 || !strings.Contains(queued[0], "the safeguards refused its conversation") || !strings.Contains(queued[0], "new session") {
		t.Fatalf("lead queue = %q", queued)
	}
}

func TestTheLeadIsToldOfEachTurnAnAPIErrorStopped(t *testing.T) {
	f := stoppedTurnFixture(t)
	f.stopTurn(t, "turn-1", &surface.TurnStop{TurnID: "turn-1", At: time.Now().Add(-time.Minute), Reason: "Codex error: exceeded retry limit, last status: 429 Too Many Requests"}, nil)
	f.stopTurn(t, "turn-2", &surface.TurnStop{TurnID: "turn-2", At: time.Now(), Reason: "Codex error: stream disconnected"}, nil)
	queued := leadQueue(t, f.r)
	if len(queued) != 2 || !strings.Contains(queued[0], "exceeded retry limit") || !strings.Contains(queued[1], "stream disconnected") || !strings.Contains(queued[1], "send it a message to retry") {
		t.Fatalf("lead queue = %q", queued)
	}
}

func TestTheLeadIsNotToldOfAUsageLimitOrAStopFromLongAgo(t *testing.T) {
	f := stoppedTurnFixture(t)
	f.stopTurn(t, "turn-1", nil, &surface.UsageLimit{ResetsAt: time.Now().Add(time.Hour)})
	f.stopTurn(t, "turn-2", &surface.TurnStop{TurnID: "turn-2", At: time.Now().Add(-48 * time.Hour), Reason: "Claude API error: overloaded"}, nil)
	if queued := leadQueue(t, f.r); len(queued) != 0 {
		t.Fatalf("lead queue = %q", queued)
	}
}
