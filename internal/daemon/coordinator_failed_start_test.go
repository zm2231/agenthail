package daemon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type goneConversationSurface struct {
	*daemonSurface
	gone map[string]bool
}

func (g goneConversationSurface) ConversationExists(_ context.Context, session *surface.Session) (bool, error) {
	return !g.gone[session.ID], nil
}

func TestACoordinatorStartThatFailsKeepsThePreviousLead(t *testing.T) {
	d, r, fake, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
		t.Fatalf("set: %d %s", response.Code, response.Body)
	}
	d.Surfaces[0] = goneConversationSurface{daemonSurface: fake, gone: map[string]bool{"from": true}}
	lead := func() string {
		t.Helper()
		spec, err := r.CoordinatorNamed("captain")
		if err != nil {
			t.Fatal(err)
		}
		id, err := r.CoordinatorSessionID(spec)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	fake.startErr = errors.New("initial message rejected")
	if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello"}`); response.Code < http.StatusMultipleChoices {
		t.Fatalf("a start that failed was reported as sent: %d %s", response.Code, response.Body)
	}
	if got := lead(); got != "from" {
		t.Fatalf("lead=%q after a failed start, want the previous lead kept", got)
	}
	if roles, err := r.LocalSessionRoles("started"); err != nil || roles.Lead {
		t.Fatalf("the failed conversation leads the project: roles=%+v err=%v", roles, err)
	}

	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out"))
	if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello again"}`); response.Code >= http.StatusMultipleChoices {
		t.Fatalf("an outcome-unknown start was refused: %d %s", response.Code, response.Body)
	}
	if got := lead(); got != "started" {
		t.Fatalf("lead=%q after an outcome-unknown start, want the new conversation", got)
	}
}
