package daemon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type goneConversationSurface struct {
	*daemonSurface
	gone map[string]bool
}

func (g goneConversationSurface) ConversationExists(_ context.Context, session *surface.Session) (bool, error) {
	return !g.gone[session.ID], nil
}

type uuidCapturingSurface struct {
	goneConversationSurface
	uuids *[]string
}

func (u uuidCapturingSurface) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	*u.uuids = append(*u.uuids, surface.MessageUUID(ctx))
	return u.daemonSurface.StartSession(ctx, options)
}

func TestAQueuedRestartMovesItsDeliveryRecordToTheNewConversation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		startErr error
		status   registry.DeliveryIntentStatus
		turn     string
		held     bool
	}{
		{name: "accepted", status: registry.DeliveryIntentSent, turn: "started-turn"},
		{name: "held before the restart", status: registry.DeliveryIntentSent, turn: "started-turn", held: true},
		{name: "outcome unknown", startErr: surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out")), status: registry.DeliveryIntentSubmitted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, r, fake, repo := projectsFixture(t)
			if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`"}`); response.Code != http.StatusOK {
				t.Fatalf("add: %d %s", response.Code, response.Body)
			}
			if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"captain"}`); response.Code != http.StatusOK {
				t.Fatalf("set: %d %s", response.Code, response.Body)
			}
			var uuids []string
			d.Surfaces[0] = uuidCapturingSurface{goneConversationSurface: goneConversationSurface{daemonSurface: fake, gone: map[string]bool{"from": true}}, uuids: &uuids}
			fake.startErr = tc.startErr
			_, intentID, err := r.QueueDeliveryWithIntent("from", "held for the coordinator", "", surface.SendOptions{SourceSessionID: "to"})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := r.DeliveryIntent(intentID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.held {
				if changed, err := r.HoldPeerMessage(registry.PeerMessage{MsgID: queued.UUID, TargetSessionID: "from", SenderSessionID: "to"}, "parked by Claude"); err != nil || !changed {
					t.Fatalf("hold changed=%v err=%v", changed, err)
				}
				if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 1 {
					t.Fatalf("held problems=%+v err=%v", problems, err)
				}
			}
			previous, err := r.Session("from")
			if err != nil {
				t.Fatal(err)
			}
			d.drainMessageQueue(context.Background(), d.Surfaces[0], previous)
			if len(uuids) != 1 || uuids[0] != queued.UUID {
				t.Fatalf("start uuids=%v, want the queued message's %s", uuids, queued.UUID)
			}
			intent, err := r.DeliveryIntent(intentID)
			if err != nil {
				t.Fatal(err)
			}
			if intent.TargetSessionID != "started" || intent.Status != tc.status || intent.ProviderKey != tc.turn {
				t.Fatalf("intent=%+v, want it on the new conversation as %s", intent, tc.status)
			}
			if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
				t.Fatalf("problems=%+v err=%v", problems, err)
			}
			if tc.turn != "" {
				if reconciled, err := r.ReconcileDeliveryIntent("started", tc.turn); err != nil || !reconciled {
					t.Fatalf("reconciled=%v err=%v", reconciled, err)
				}
			}
		})
	}
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

	if _, _, err := r.QueueDeliveryWithIntent("from", "held for the coordinator", "", surface.SendOptions{SourceSessionID: "to"}); err != nil {
		t.Fatal(err)
	}
	previous, err := r.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(context.Background(), d.Surfaces[0], previous)
	if starts := len(fake.startOptions); starts != 2 {
		t.Fatalf("starts=%d, want one start for the queued message", starts)
	}
	if got, queued := lead(), r.QueueCount("from"); got != "from" || queued != 0 {
		t.Fatalf("lead=%q queued=%d, want the previous lead kept and the message settled, not retried", got, queued)
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil {
		t.Fatal(err)
	}
	perSession := map[string]int{}
	for _, problem := range problems {
		perSession[problem.SessionID]++
	}
	if perSession["started"] != 1 || perSession["from"] != 1 || len(problems) != 2 {
		t.Fatalf("problems=%+v, want one per failed message", problems)
	}

	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out"))
	if response := runDashboardAction(t, d, `{"action":"coordinator-send","name":"captain","message":"hello again"}`); response.Code >= http.StatusMultipleChoices {
		t.Fatalf("an outcome-unknown start was refused: %d %s", response.Code, response.Body)
	}
	if got := lead(); got != "started" {
		t.Fatalf("lead=%q after an outcome-unknown start, want the new conversation", got)
	}
}
