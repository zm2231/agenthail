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
	if refreshed.Activity == nil || !refreshed.Activity.BusySince.Equal(started) {
		t.Fatalf("a two-hour turn reads as working since %+v, it started %s", refreshed.Activity, started)
	}
}

func TestALongClaudeTurnAStopHookSentBackStaysWorkingSinceItsPrompt(t *testing.T) {
	shrinkClaudeObservationWindow(t)
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	started := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	sentBack := started.Add(30 * time.Minute)
	path := writeTranscript(t, `{"type":"user","uuid":"prompt","timestamp":"`+started.Format(time.RFC3339)+`","message":{"role":"user","content":"keep going"}}`)
	appendTranscript(t, path, `{"type":"assistant","uuid":"a1","timestamp":"`+started.Add(time.Minute).Format(time.RFC3339)+`","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}
{"type":"user","uuid":"r1","timestamp":"`+started.Add(2*time.Minute).Format(time.RFC3339)+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
{"type":"user","uuid":"feedback","isMeta":true,"timestamp":"`+sentBack.Format(time.RFC3339)+`","message":{"role":"user","content":"Stop hook feedback:\nfix the type errors before stopping"}}
{"type":"system","subtype":"stop_hook_summary","hookCount":1,"hookErrors":["fix the type errors before stopping"],"preventedContinuation":false,"timestamp":"`+sentBack.Format(time.RFC3339)+`"}
`)
	progress := `{"type":"assistant","uuid":"p","timestamp":"` + sentBack.Add(time.Minute).Format(time.RFC3339) + `","message":{"id":"m2","content":[{"type":"text","text":"` + strings.Repeat("x", 2000) + `"}]}}` + "\n"
	var filler strings.Builder
	for int64(filler.Len()) < 2*initialClaudeObservationBytes {
		filler.WriteString(progress)
	}
	appendTranscript(t, path, filler.String())
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "updatedAt": float64(started.UnixMilli()), "statusUpdatedAt": float64(started.UnixMilli()), "peerFeatures": []any{"notify_idle"}})
	session := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true, LastActive: started}

	refreshed, err := claude.LocalStatus(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Activity == nil || !refreshed.Activity.BusySince.Equal(started) {
		t.Fatalf("a turn a Stop hook sent back reads as working since %+v, its prompt was at %s", refreshed.Activity, started)
	}
}

func TestAClaudeTurnWithoutItsPromptAfterAFinishedTurnKeepsTheFirstRowItSaw(t *testing.T) {
	shrinkClaudeObservationWindow(t)
	home := t.TempDir()
	claude := NewClaude(nil, home)
	pid := os.Getpid()
	earlier := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	path := writeTranscript(t, `{"type":"user","uuid":"old","timestamp":"`+earlier.Format(time.RFC3339)+`","message":{"role":"user","content":"earlier"}}`)
	appendTranscript(t, path, `{"type":"system","subtype":"turn_duration","timestamp":"`+earlier.Add(time.Minute).Format(time.RFC3339)+`"}`+"\n")
	first := earlier.Add(time.Hour)
	progress := `{"type":"assistant","uuid":"p","timestamp":"` + first.Format(time.RFC3339) + `","message":{"id":"m","content":[{"type":"text","text":"` + strings.Repeat("x", 2000) + `"}]}}` + "\n"
	var filler strings.Builder
	for int64(filler.Len()) < 2*initialClaudeObservationBytes {
		filler.WriteString(progress)
	}
	appendTranscript(t, path, filler.String())
	writePeerRecord(t, home, pid, map[string]any{"pid": pid, "sessionId": "conversation-1", "status": "busy", "peerFeatures": []any{"notify_idle"}})
	session := surface.Session{ID: "conversation-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: pid, Status: surface.StatusBusy, Transcript: path, HasLocal: true}
	refreshed, err := claude.LocalStatus(context.Background(), session)
	if err != nil || refreshed.Activity == nil || !refreshed.Activity.BusySince.Equal(first) {
		t.Fatalf("a finished turn's prompt was taken as this turn's start: activity=%+v err=%v", refreshed.Activity, err)
	}
}
