package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAMessageThatWaitedInTheQueueSaysWhenItWasSent(t *testing.T) {
	for _, c := range []struct {
		message string
		waited  time.Duration
		want    string
	}{
		{"stale news", 25 * time.Minute, "(sent 25 min ago) stale news"},
		{"[from @lead] check the build", 25 * time.Minute, "[from @lead] (sent 25 min ago) check the build"},
		{"fresh", time.Minute, "fresh"},
	} {
		f := newHumanTurnFixture(t)
		if _, err := f.r.QueueMessageWithOptions(f.bridged.ID, c.message, "", surface.SendOptions{SourceSessionID: "agent", SentAt: time.Now().Add(-c.waited)}); err != nil {
			t.Fatal(err)
		}
		f.d.drainMessageQueue(t.Context(), f.claude, &f.bridged)
		if len(f.claude.native) != 1 || f.claude.native[0] != c.want {
			t.Fatalf("delivered %q, want %q", f.claude.native, c.want)
		}
	}
}

func TestALocalSendCannotClaimToBeOlder(t *testing.T) {
	f := newHumanTurnFixture(t)
	f.transcript(t, f.bridged, idleClaudeTranscript+busyClaudeTranscript)
	sent := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339Nano)
	receipt := f.send(t, `{"action":"send","sessionId":"conversation-bridge","busyDelivery":"queue","message":"fresh news","sentAt":"`+sent+`"}`)
	if receipt["evidence"] != string(surface.EvidenceQueued) {
		t.Fatalf("receipt=%v", receipt)
	}
	f.transcript(t, f.bridged, idleClaudeTranscript)
	f.d.drainMessageQueue(t.Context(), f.claude, &f.bridged)
	if len(f.claude.native) != 1 || f.claude.native[0] != "fresh news" {
		t.Fatalf("native=%q posts=%q", f.claude.native, f.posts)
	}
}

type firstTurnCapturingSurface struct {
	goneConversationSurface
	first *[]string
}

func (c firstTurnCapturingSurface) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	*c.first = append(*c.first, options.Message)
	return c.daemonSurface.StartSession(ctx, options)
}

func TestAStaleMessageThatRestartsAGoneLeadSaysWhenItWasSent(t *testing.T) {
	d, r, fake, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body)
	}
	var first []string
	d.Surfaces[0] = firstTurnCapturingSurface{goneConversationSurface: goneConversationSurface{daemonSurface: fake, gone: map[string]bool{"from": true}}, first: &first}
	pastGoneGrace(d)
	if _, err := r.QueueMessageWithOptions("from", "is the deploy done?", "", surface.SendOptions{SourceSessionID: "to", SentAt: time.Now().Add(-40 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	previous, err := r.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(context.Background(), d.Surfaces[0], previous)
	if len(first) != 1 || !strings.HasSuffix(first[0], "\n\n(sent 40 min ago) is the deploy done?") {
		t.Fatalf("the restarted lead's first turn=%q", first)
	}
	history, err := r.ListHistory(20, "from")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if strings.Contains(entry.Message, "(sent ") {
			t.Fatalf("history recorded the delivery marker: %+v", entry)
		}
	}
}
