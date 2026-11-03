package surfaces

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAClaudeTurnLongerThanTheFirstReadKeepsItsNewestRowAsLastActivity(t *testing.T) {
	shrinkClaudeObservationWindow(t)
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	started := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	path := writeTranscript(t, `{"type":"user","uuid":"prompt","timestamp":"`+started.Format(time.RFC3339)+`","message":{"role":"user","content":"keep going"}}`)
	progress := `{"type":"assistant","uuid":"p","timestamp":"` + started.Add(time.Minute).Format(time.RFC3339) + `","message":{"id":"m","content":[{"type":"text","text":"` + strings.Repeat("x", 2000) + `"}]}}` + "\n"
	var filler strings.Builder
	for int64(filler.Len()) < 2*initialClaudeObservationBytes {
		filler.WriteString(progress)
	}
	appendTranscript(t, path, filler.String())
	newest := time.Now().Add(-5 * time.Second).UTC().Truncate(time.Second)
	appendTranscript(t, path, `{"type":"assistant","uuid":"last","timestamp":"`+newest.Format(time.RFC3339)+`","message":{"id":"m2","content":[{"type":"text","text":"still working"}]}}`+"\n")
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "updatedAt": float64(started.UnixMilli()), "statusUpdatedAt": float64(started.UnixMilli()), "peerFeatures": []any{"notify_idle"}})
	session := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true, LastActive: started}

	refreshed, err := claude.LocalStatus(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.LastActive.Before(newest) {
		t.Fatalf("a working Claude turn reads last active %s, its newest row is %s (activity %+v)", refreshed.LastActive, newest, refreshed.Activity)
	}
}
