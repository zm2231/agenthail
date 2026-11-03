package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type goneConversationSurface struct {
	*daemonSurface
	gone map[string]bool
}

func (g goneConversationSurface) ConversationState(_ context.Context, session *surface.Session) (surface.ConversationState, error) {
	if g.gone[session.ID] {
		return surface.ConversationGone, nil
	}
	return surface.ConversationLive, nil
}

func pastGoneGrace(d *Daemon) {
	d.coordinatorNow = func() time.Time { return time.Now().Add(coordinator.GoneGrace + time.Minute) }
}

type uuidCapturingSurface struct {
	goneConversationSurface
	uuids *[]string
}

func (u uuidCapturingSurface) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	*u.uuids = append(*u.uuids, surface.MessageUUID(ctx))
	return u.daemonSurface.StartSession(ctx, options)
}

func catalogEventsOfType(t *testing.T, events <-chan registry.CatalogEvent, kind string) int {
	t.Helper()
	count := 0
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatal("catalog subscriber closed")
			}
			if event.Type == kind {
				count++
			}
		case <-time.After(300 * time.Millisecond):
			return count
		}
	}
}

func TestAQueuedRestartMovesItsDeliveryRecordToTheNewConversation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		startErr error
		status   registry.DeliveryIntentStatus
		turn     string
		before   string
	}{
		{name: "accepted", status: registry.DeliveryIntentSent, turn: "started-turn"},
		{name: "held before the restart", status: registry.DeliveryIntentSent, turn: "started-turn", before: "held"},
		{name: "failed before the restart", status: registry.DeliveryIntentSent, turn: "started-turn", before: "failed"},
		{name: "failure dismissed before the restart", status: registry.DeliveryIntentSent, turn: "started-turn", before: "dismissed"},
		{name: "outcome unknown", startErr: surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out")), status: registry.DeliveryIntentSubmitted},
		{name: "held, outcome unknown", startErr: surface.DeliveryOutcomeUnknown(errors.New("turn/start timed out")), status: registry.DeliveryIntentSubmitted, before: "held"},
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
			pastGoneGrace(d)
			fake.startErr = tc.startErr
			_, intentID, err := r.QueueDeliveryWithIntent("from", "held for the coordinator", "", surface.SendOptions{SourceSessionID: "to"})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := r.DeliveryIntent(intentID)
			if err != nil {
				t.Fatal(err)
			}
			peer := registry.PeerMessage{MsgID: queued.UUID, TargetSessionID: "from", SenderSessionID: "to"}
			switch tc.before {
			case "held":
				if changed, err := r.HoldPeerMessage(peer, "parked by Claude"); err != nil || !changed {
					t.Fatalf("hold changed=%v err=%v", changed, err)
				}
			case "failed", "dismissed":
				if changed, err := r.DropPeerMessage(peer, "dropped by Claude"); err != nil || !changed {
					t.Fatalf("drop changed=%v err=%v", changed, err)
				}
				if tc.before == "dismissed" {
					if changed, err := r.DismissDeliveryProblem(intentID); err != nil || !changed {
						t.Fatalf("dismiss changed=%v err=%v", changed, err)
					}
				}
			}
			if tc.before != "" && r.QueueCount("to") == 0 {
				t.Fatalf("%s left the sender no notice to retire", tc.before)
			}
			if err := d.catalog.flushCommitted(); err != nil {
				t.Fatal(err)
			}
			_, latest, err := r.CatalogState()
			if err != nil {
				t.Fatal(err)
			}
			_, follower, err := d.catalog.subscribe(latest)
			if err != nil {
				t.Fatal(err)
			}
			events := followCatalog(t, follower)
			defer follower.Cancel()
			previous, err := r.Session("from")
			if err != nil {
				t.Fatal(err)
			}
			d.drainMessageQueue(context.Background(), d.Surfaces[0], previous)
			window, err := r.CatalogEventsAfter(latest, 1000)
			if err != nil {
				t.Fatal(err)
			}
			d.catalog.mu.Lock()
			published := d.catalog.publishedSeq
			d.catalog.mu.Unlock()
			for _, event := range window.Events {
				if event.Type == "delivery.dismissed" && event.Seq > published {
					t.Fatalf("dismissal %d committed but not published (published through %d)", event.Seq, published)
				}
			}
			if dismissals := catalogEventsOfType(t, events, "delivery.dismissed"); (tc.before == "held" || tc.before == "failed") != (dismissals == 1) || dismissals > 1 {
				t.Fatalf("live subscriber saw %d dismissals, want one only for an open problem", dismissals)
			}
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
			rows, err := r.ListQueue(false, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.SessionID == "to" && !strings.HasPrefix(row.Message, "[agenthail] Your message to @captain started a new conversation") {
					t.Fatalf("the sender still has a notice about a message the new conversation took: %+v", row)
				}
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
	pastGoneGrace(d)
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
