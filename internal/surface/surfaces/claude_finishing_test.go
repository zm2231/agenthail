package surfaces

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func finishingFixture(t *testing.T, endedAt time.Time) (*Claude, *surface.Session, string, string) {
	t.Helper()
	home := t.TempDir()
	pid := os.Getpid()
	stamp := endedAt.UTC().Format(time.RFC3339Nano)
	path := writeTranscript(t, `
{"type":"user","uuid":"prompt","timestamp":"`+endedAt.Add(-time.Minute).UTC().Format(time.RFC3339Nano)+`","message":{"role":"user","content":"ship it"}}
{"type":"assistant","uuid":"a1","timestamp":"`+stamp+`","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"Shipped."}]}}`)
	session := &surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true}
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "peerFeatures": []any{"notify_idle"}})
	return NewClaude(nil, home), session, home, path
}

func TestABusyClaudeProcessAfterItsReplyEndedIsFinishingOnceTheGracePasses(t *testing.T) {
	endedAt := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	claude, session, _, _ := finishingFixture(t, endedAt)
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.Activity == nil || !observation.Activity.FinishingSince.Equal(endedAt) {
		t.Fatalf("observation=%+v activity=%+v err=%v; want busy, finishing since %s", observation, observation.Activity, err, endedAt)
	}
	refreshed, err := claude.LocalStatus(context.Background(), *session)
	if err != nil || refreshed.Activity == nil || !refreshed.Activity.FinishingSince.Equal(endedAt) {
		t.Fatalf("local status activity=%+v err=%v", refreshed.Activity, err)
	}
}

func TestAReplyThatJustEndedIsNotFinishingYetButFallsDue(t *testing.T) {
	endedAt := time.Now().Truncate(time.Millisecond)
	claude, session, _, _ := finishingFixture(t, endedAt)
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Activity == nil || !observation.Activity.FinishingSince.IsZero() || !observation.Activity.EndedAt.Equal(endedAt) {
		t.Fatalf("activity=%+v err=%v; want ended at %s, not finishing", observation.Activity, err, endedAt)
	}
	if observation.Activity.FinishingDue(endedAt.Add(surface.FinishingGrace - time.Millisecond)) {
		t.Fatal("finishing fell due inside the grace")
	}
	if !observation.Activity.FinishingDue(endedAt.Add(surface.FinishingGrace)) {
		t.Fatal("finishing did not fall due once the grace ended")
	}
}

func TestFinishingEndsWhenAStopHookContinuesTheTurnOrTheProcessGoesIdle(t *testing.T) {
	endedAt := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	claude, session, home, path := finishingFixture(t, endedAt)
	appendTranscript(t, path, `{"type":"assistant","uuid":"a2","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","message":{"id":"m2","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{}}]}}`+"\n")
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.Activity == nil || !observation.Activity.FinishingSince.IsZero() || observation.Activity.Tool == nil {
		t.Fatalf("continued turn activity=%+v err=%v; want working on Bash", observation.Activity, err)
	}

	pid := os.Getpid()
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "idle", "peerFeatures": []any{"notify_idle"}})
	if observation, err = claude.Observe(context.Background(), session); err != nil || observation.Status != surface.StatusIdle || observation.Activity != nil {
		t.Fatalf("idle record observation=%+v err=%v", observation, err)
	}
}

func TestAProcessWaitingOnAPromptAfterItsReplyIsNotFinishing(t *testing.T) {
	endedAt := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	claude, session, home, _ := finishingFixture(t, endedAt)
	pid := os.Getpid()
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "waiting", "waitingFor": "approve Bash", "statusUpdatedAt": float64(time.Now().UnixMilli()), "peerFeatures": []any{"notify_idle"}})
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.WaitingOn == nil || (observation.Activity != nil && !observation.Activity.FinishingSince.IsZero()) {
		t.Fatalf("waiting observation=%+v activity=%+v err=%v", observation, observation.Activity, err)
	}
}
