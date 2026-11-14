package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestAPeerMessageIsDeliveredWhenTheTurnThatReadItEndsOnASessionNothingElseWatches(t *testing.T) {
	peer := func(id string) string {
		return `{"type":"user","uuid":"claude-own","timestamp":"2026-11-14T10:01:00Z","isMeta":true,"origin":{"kind":"peer","msg_id":"` + id + `","from":"uds:/tmp/cc-socks/1.sock"},"message":{"content":"Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/cc-socks/1.sock\" from-mode=\"prompting\">\nhello\n</cross-session-message>\n\nThis came from another Claude session."}}` + "\n"
	}
	queued := func(id string) string {
		return `{"type":"attachment","uuid":"q1","timestamp":"2026-11-14T10:01:10Z","attachment":{"type":"queued_command","prompt":"hello","origin":{"kind":"peer","msg_id":"` + id + `"},"source_uuid":"claude-own"}}` + "\n"
	}
	working := `{"type":"user","uuid":"u1","timestamp":"2026-11-14T10:01:00Z","origin":{"kind":"human"},"message":{"content":"run the build"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-11-14T10:01:01Z","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make"}}]}}
`
	ended := `{"type":"user","uuid":"r1","timestamp":"2026-11-14T10:01:20Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}
{"type":"assistant","uuid":"a2","timestamp":"2026-11-14T10:01:21Z","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"got it"}]}}
{"type":"system","subtype":"stop_hook_summary","uuid":"s1","timestamp":"2026-11-14T10:01:22Z"}
`
	for _, tc := range []struct {
		name string
		busy string
		read func(id string) string
		end  string
	}{
		{name: "an idle session's turn it starts", read: peer, end: ended[strings.Index(ended, "\n")+1:]},
		{name: "the running turn Claude queues it into", busy: working, read: queued, end: ended},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			transcript := filepath.Join(home, "target.jsonl")
			appendLines := func(lines string) {
				t.Helper()
				f, err := os.OpenFile(transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := f.WriteString(lines); err != nil {
					t.Fatal(err)
				}
			}
			appendLines(`{"type":"user","uuid":"u0","timestamp":"2026-11-14T10:00:00Z","origin":{"kind":"human"},"message":{"content":"hi"}}
{"type":"assistant","uuid":"a0","timestamp":"2026-11-14T10:00:01Z","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"hello"}]}}
{"type":"system","subtype":"stop_hook_summary","uuid":"s0","timestamp":"2026-11-14T10:00:02Z"}
` + tc.busy)
			r := hostRegistry(t)
			for _, session := range []surface.Session{
				{ID: "sender", Name: "sender", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle},
				{ID: "target", Name: "target", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transport: "uds", PID: os.Getpid(), Transcript: transcript, Cwd: home, Status: surface.StatusIdle},
			} {
				if err := r.RegisterSession(session); err != nil {
					t.Fatal(err)
				}
			}
			d := newTestDaemon(t, r, []surface.Surface{surfaces.NewClaude(nil, home)})
			d.log = log.New(io.Discard, "", 0)
			intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: "hello", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
			if err != nil {
				t.Fatal(err)
			}
			if marked, err := r.MarkDeliveryIntentSent(intent.ID, intent.UUID, surface.EvidenceTransportAccepted); err != nil || !marked {
				t.Fatalf("marked=%v err=%v", marked, err)
			}
			d.scanAndRelay(context.Background())
			appendLines(tc.read(intent.UUID))
			d.scanAndRelay(context.Background())
			if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != registry.DeliveryIntentSent {
				t.Fatalf("intent=%+v err=%v, want sent until the turn that read it ends", got, err)
			}
			appendLines(tc.end)
			d.scanAndRelay(context.Background())
			if got, err := r.DeliveryIntent(intent.ID); err != nil || got.Status != registry.DeliveryIntentDelivered {
				t.Fatalf("intent=%+v err=%v, want delivered once the turn that read it ended", got, err)
			}
			watched, err := r.WatchedSessions()
			if err != nil {
				t.Fatal(err)
			}
			for _, session := range watched {
				if session.ID == "target" {
					t.Fatalf("the target is still watched after its delivery completed: %+v", watched)
				}
			}
		})
	}
}
