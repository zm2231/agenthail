package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// slowPoll raises the stream timer far above the wake bound the tests assert,
// so an event inside the bound can only have come from a file wake.
func slowPoll(t *testing.T) {
	t.Helper()
	original := transcriptPollInterval
	transcriptPollInterval = 10 * time.Second
	t.Cleanup(func() { transcriptPollInterval = original })
}

const wakeBound = time.Second

// streamWaits counts the times a stream has read its transcript and started
// to wait. A write after the count moves past a value reaches the stream only
// through a wake or the slow timer.
func streamWaits(t *testing.T) *atomic.Int64 {
	t.Helper()
	waits := &atomic.Int64{}
	transcriptWaiting = func() { waits.Add(1) }
	t.Cleanup(func() { transcriptWaiting = nil })
	return waits
}

func awaitWaitBeyond(t *testing.T, waits *atomic.Int64, seen int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for waits.Load() <= seen {
		if time.Now().After(deadline) {
			t.Fatal("the stream never started to wait")
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitWokenText(t *testing.T, texts <-chan string, want string, since time.Time) {
	t.Helper()
	deadline := time.After(wakeBound)
	for {
		select {
		case text := <-texts:
			if strings.Contains(text, want) {
				t.Logf("%q reached the stream after %s", want, time.Since(since).Round(time.Millisecond))
				return
			}
		case <-deadline:
			t.Fatalf("%q did not reach the stream within %s", want, wakeBound)
		}
	}
}

func claudeAssistantText(id, text string) string {
	return `{"type":"assistant","uuid":"` + id + `","message":{"id":"m-` + id + `","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestAClaudeStreamWakesOnAnAppendAndFollowsAReplacedTranscript(t *testing.T) {
	slowPoll(t)
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"hello"}}`)
	session := seededClaudeStreamSession(t, path, "")
	waits := streamWaits(t)
	texts := make(chan string, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Text != "" {
				texts <- event.Text
			}
		}, time.Minute)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	awaitWaitBeyond(t, waits, 0)

	seen := waits.Load()
	appended := time.Now()
	testkit.AppendLines(t, path, claudeAssistantText("a1", "appended"))
	awaitWokenText(t, texts, "appended", appended)
	awaitWaitBeyond(t, waits, seen)

	seen = waits.Load()
	replaced := time.Now()
	replaceTranscript(t, path, `{"type":"user","uuid":"r1","message":{"content":"after the replacement"}}`)
	awaitWokenText(t, texts, "after the replacement", replaced)
	awaitWaitBeyond(t, waits, seen)

	later := time.Now()
	testkit.AppendLines(t, path, claudeAssistantText("a2", "appended to the replacement"))
	awaitWokenText(t, texts, "appended to the replacement", later)
}

func TestACodexStreamWakesOnAnAppend(t *testing.T) {
	slowPoll(t)
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
	waits := streamWaits(t)
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
	awaitWaitBeyond(t, waits, 0)
	appended := time.Now()
	testkit.AppendLines(t, path,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"woken by the append"}]}}`)
	awaitWokenText(t, texts, "woken by the append", appended)
}
