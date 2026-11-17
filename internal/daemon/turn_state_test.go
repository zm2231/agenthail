package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAJudgedTurnShowsItsStateOnTheRowWithoutRaisingAttention(t *testing.T) {
	d, _, fake, from := judgeFixture(t, &judgeStub{}, "")
	finishJudgedTurn(d, fake, &from, "turn-1", "I fixed the parser. I'll trace the flaky test now.", nil)
	eventually(t, "the judgment", func() bool {
		judgments := readJudgments(t, d, from.ID).Judgments
		return len(judgments) == 1 && !judgments[0].JudgedAt.IsZero()
	})
	for name, row := range map[string]dashboardSession{"view": viewRow(t, d, from.ID), "snapshot": snapshotSession(t, dashboardRouter(d), from.ID)} {
		if row.TurnState == nil || row.TurnState.State != turnContinuing || row.TurnState.Source != turnSourceJudge {
			t.Fatalf("%s: the judged turn state = %+v", name, row.TurnState)
		}
		if row.Display == nil || row.Display.Kind != "idle" || row.Display.Label != "Said it would continue" || row.Attention != nil {
			t.Fatalf("%s: display %+v, attention %+v", name, row.Display, row.Attention)
		}
	}
}

func judgeTurnOf(t *testing.T, node *federationNode, sessionID, turnID string, judgment registry.TurnJudgment) {
	t.Helper()
	if err := node.reg.SaveRuntimeState(sessionID, surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turnID}); err != nil {
		t.Fatal(err)
	}
	if state := viewRow(t, node.daemon, sessionID).TurnState; state != nil && state.Source != turnSourceEndTurn && state.Source != turnSourceFacts {
		t.Fatalf("%s shows a judged state before its turn %s is judged: %+v", sessionID, turnID, state)
	}
	judgment.SessionID, judgment.TurnID, judgment.EndedAt, judgment.JudgedAt = sessionID, turnID, time.Now(), time.Now()
	node.daemon.recordTurnJudgment(judgment)
}

func TestAWorkerWaitsOnItsLeadWhereTheLeadWaitsOnThePerson(t *testing.T) {
	mac, vps, record := widgetOnTwoHosts(t)
	label := func(id string) (string, *dashboardTurnState) {
		t.Helper()
		row := viewRow(t, mac.daemon, id)
		return row.Display.Label, row.TurnState
	}

	judgeTurnOf(t, mac, "local-a", "a-1", registry.TurnJudgment{State: turnWaitingPerson})
	if got, state := label("local-a"); got != "Waiting on @widget-lead" || state.Source != turnSourceJudge {
		t.Fatalf("a worker the judge says waits on the person reads %q (%+v)", got, state)
	}
	judgeTurnOf(t, mac, "lead", "lead-1", registry.TurnJudgment{State: turnWaitingPerson})
	if got, _ := label("lead"); got != "Needs you" {
		t.Fatalf("the lead waiting on the person reads %q", got)
	}

	judgeTurnOf(t, mac, "local-a", "a-2", registry.TurnJudgment{State: turnDone, Facts: registry.TurnFacts{Awaiting: []string{"lead"}, AwaitedRelays: 1}})
	if got, state := label("local-a"); got != "Waiting on @widget-lead" || state.Source != turnSourceFacts {
		t.Fatalf("a worker whose turn ended awaiting its lead's reply reads %q (%+v)", got, state)
	}
	judgeTurnOf(t, mac, "local-a", "a-3", registry.TurnJudgment{State: turnDone, Facts: registry.TurnFacts{BackgroundRunning: true}})
	if got, _ := label("local-a"); got != "Waiting on background work" {
		t.Fatalf("a turn that ended with background work running reads %q", got)
	}
	judgeTurnOf(t, mac, "local-a", "a-4", registry.TurnJudgment{State: turnDone})
	if got, _ := label("local-a"); got != "Done" {
		t.Fatalf("a judged finished turn reads %q", got)
	}
	judgeTurnOf(t, mac, "local-a", "a-5", registry.TurnJudgment{State: turnWaitingWork, Error: "judge down"})
	if got, state := label("local-a"); got == "Waiting" || state != nil {
		t.Fatalf("a failed judgment with no end_turn reads %q (%+v)", got, state)
	}

	select {
	case <-vps.daemon.catalogNudge:
	default:
	}
	judgeTurnOf(t, vps, "remote-c", "c-1", registry.TurnJudgment{State: turnWaitingPerson})
	select {
	case <-vps.daemon.catalogNudge:
	default:
		t.Fatal("a judgment did not ask the host to publish the row again")
	}
	vps.daemon.discoverCatalog(t.Context())
	mirrored := hosts.Qualify(record.ID, "remote-c")
	testkit.Eventually(t, "the viewer to show the host's judged state", 10*time.Second, func() bool {
		view, err := mac.daemon.readSessionView(t.Context(), sessionViewRequest{Scope: "all", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range view.Sessions {
			if row.ID == mirrored {
				return row.Display != nil && row.Display.Label == "Waiting on @vps-lead"
			}
		}
		return false
	})
}
