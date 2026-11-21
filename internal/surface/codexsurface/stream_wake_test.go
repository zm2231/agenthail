package codexsurface

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestACodexStreamFollowsAReplacedTranscript(t *testing.T) {
	surfacetest.SlowPoll(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"old"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "review", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy, TranscriptIdentity: "old-file", TranscriptOffsetSet: true, TranscriptOffset: int64(len(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"old"}}` + "\n")), CodexCurrentTurnID: "old", CodexPendingEventUser: true, CodexPendingEventTurn: "old"}
	texts := make(chan string, 1)
	waits := surfacetest.StreamWaits(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = codex.Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Text != "" {
				texts <- event.Text
			}
		}, time.Minute)
	}()
	t.Cleanup(func() { cancel(); <-done })
	surfacetest.AwaitWaitBeyond(t, waits, 0)
	testkit.AppendLines(t, path, `{"type":"event_msg","payload":{"type":"user_message"}}`)
	surfacetest.AwaitWaitBeyond(t, waits, 1)
	surfacetest.ReplaceTranscript(t, path, `{"type":"event_msg","payload":{"type":"task_started","turn_id":"new"}}`, `{"type":"response_item","payload":{"id":"user-new","type":"message","role":"user","content":[{"type":"input_text","text":"after replacement"}]}}`)
	surfacetest.AwaitWokenText(t, texts, "after replacement", time.Now().Add(-time.Second))
}

func TestACodexStreamWakesOnAnAppend(t *testing.T) {
	surfacetest.SlowPoll(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "review", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy}
	seed, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil || !seed.TranscriptOffsetSet {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
	streamSession := *session
	streamSession.TranscriptOffset = seed.TranscriptOffset
	streamSession.TranscriptOffsetSet = true
	waits := surfacetest.StreamWaits(t)
	texts := make(chan string, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		codex.Stream(ctx, &streamSession, "", func(event surface.StreamEvent) {
			if event.Text != "" {
				texts <- event.Text
			}
		}, time.Minute)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	surfacetest.AwaitWaitBeyond(t, waits, 0)
	appended := time.Now()
	testkit.AppendLines(t, path,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"woken by the append"}]}}`)
	surfacetest.AwaitWokenText(t, texts, "woken by the append", appended)
}
