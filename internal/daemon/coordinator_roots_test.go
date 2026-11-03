package daemon

import (
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestALeadSeenMomentsAgoIsNotGoneEvenWhenNothingOfItIsFound(t *testing.T) {
	d, r, fake, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body)
	}
	d.Surfaces[0] = goneConversationSurface{daemonSurface: fake, gone: map[string]bool{"from": true}}
	if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello"}`); response.Code >= http.StatusMultipleChoices {
		t.Fatalf("send: %d %s", response.Code, response.Body)
	}
	if starts := len(fake.startOptions); starts != 0 {
		t.Fatalf("starts=%d, want none inside the grace", starts)
	}
	if queued := r.QueueCount("from"); queued != 1 {
		t.Fatalf("queued=%d, want the message waiting for the lead", queued)
	}
	pastGoneGrace(d)
	if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello again"}`); response.Code >= http.StatusMultipleChoices {
		t.Fatalf("send: %d %s", response.Code, response.Body)
	}
	if starts := len(fake.startOptions); starts != 1 {
		t.Fatalf("starts=%d, want one once the lead is gone past the grace", starts)
	}
}

func TestALeadInTheStandardRootIsNotGoneToASenderInAnotherRoot(t *testing.T) {
	f := newCoordinatorFixture(t)
	f.claude.Transcript(goneCoordinator, f.host.Cwd)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := surface.RecordDaemonClaudeRoot(f.claude.Home); err != nil {
		t.Fatal(err)
	}
	other := f.claude.AddRoot("other-root")
	t.Setenv("CLAUDE_CONFIG_DIR", other)

	if code, body := f.send(t, "lead-from-other-root", goneCoordinator, "status?"); code >= 300 {
		t.Fatalf("status=%d body=%v", code, body)
	}
	sessions, err := f.r.ListSessions(1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID != goneCoordinator && session.Surface == surface.KindClaude {
			t.Errorf("session %s (%s) exists, want no new conversation", session.ID, session.RuntimeKind)
		}
	}
	if current := f.current(t); current != goneCoordinator {
		t.Fatalf("lead=%s, want the lead kept", current)
	}
}
