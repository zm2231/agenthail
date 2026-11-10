package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var decisionStart = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func decisionRow(id, kind string, minute int) dashboardSession {
	row := dashboardSession{ID: id, Display: &SessionDisplay{Kind: "idle"}, Project: &CatalogProject{ID: "web-app"}, LastActive: decisionStart.Add(time.Duration(minute) * time.Minute)}
	if kind == "" {
		return row
	}
	row.Attention = &SessionAttention{Kind: kind, Reason: kind + " reason"}
	if kind == "request" {
		row.WaitingOn = &surface.Waiting{ID: id + "-wait", Kind: surface.WaitingApproval, Since: row.LastActive}
	}
	return row
}

type decisionWorld struct {
	rows     []dashboardSession
	hearers  map[string]decisionHearer
	open     map[string][]openRequestView
	failures map[string]error
	queued   map[string][]string
	asked    []string
}

func (w *decisionWorld) page() decisionsPage {
	return buildDecisions(decisionSources{
		rows: w.rows,
		hearer: func(row dashboardSession) (decisionHearer, bool) {
			hearer, ok := w.hearers[row.ID]
			return hearer, ok
		},
		requests: func(rows []dashboardSession) map[string]decisionRequests {
			read := map[string]decisionRequests{}
			for _, row := range rows {
				w.asked = append(w.asked, row.ID)
				read[row.ID] = decisionRequests{requests: w.open[row.ID], err: w.failures[row.ID]}
			}
			return read
		},
		queued: w.queued,
	})
}

func prompt(id string) openRequestView {
	return openRequestView{Request: surface.Request{ID: id, Kind: surface.WaitingApproval, Tool: "Bash"}, Prompt: "Bash: make release"}
}

func groupsOf(page decisionsPage) map[string]string {
	groups := map[string]string{}
	for _, item := range page.Decisions {
		groups[item.SessionID] = item.Group
	}
	return groups
}

func TestDecisionsGroupStoppedThenHoldingThenWaitingOldestFirst(t *testing.T) {
	w := &decisionWorld{
		rows:   []dashboardSession{decisionRow("waits-late", "request", 9), decisionRow("waits-early", "request", 2), decisionRow("blocks-others", "ask", 5), decisionRow("limited", "usageLimit", 7), decisionRow("idle", "", 1)},
		open:   map[string][]openRequestView{"waits-late": {prompt("a")}, "waits-early": {prompt("b")}, "blocks-others": {prompt("c")}},
		queued: map[string][]string{"blocks-others": {"docs-writer", "test-runner"}},
	}
	page := w.page()
	var order []string
	for _, item := range page.Decisions {
		order = append(order, item.Group+":"+item.SessionID)
	}
	want := []string{"stopped:limited", "holding:blocks-others", "waiting:waits-early", "waiting:waits-late"}
	if len(order) != len(want) {
		t.Fatalf("order=%v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v want %v", order, want)
		}
	}
	if holds := page.Decisions[1].HoldsUp; len(holds) != 2 || holds[0] != "docs-writer" {
		t.Fatalf("holdsUp=%v", holds)
	}
	if !page.Decisions[2].Answerable || len(page.Decisions[2].Requests) != 1 {
		t.Fatalf("a waiting decision carries its request inline: %+v", page.Decisions[2])
	}
}

func TestSeveralRequestsOnOneSessionAreOneDecision(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("asks-twice", "request", 1)}, open: map[string][]openRequestView{"asks-twice": {prompt("first"), prompt("second")}}}
	page := w.page()
	if len(page.Decisions) != 1 || len(page.Decisions[0].Requests) != 2 {
		t.Fatalf("decisions=%+v", page.Decisions)
	}
}

func TestARequestAnsweredElsewhereIsNotListedBeforeTheCatalogCatchesUp(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("answered", "request", 1)}, open: map[string][]openRequestView{}}
	if page := w.page(); len(page.Decisions) != 0 {
		t.Fatalf("decisions=%+v", page.Decisions)
	}
}

func TestARequestWhoseProcessIsGoneIsStoppedAndNotAnswerable(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("gone", "request", 1)}, failures: map[string]error{"gone": errors.New("the session's process exited")}}
	page := w.page()
	if len(page.Decisions) != 1 || page.Decisions[0].Group != decisionStopped || page.Decisions[0].Answerable || page.Decisions[0].RequestError == "" {
		t.Fatalf("decisions=%+v", page.Decisions)
	}
}

func TestATerminalOnlyPromptIsStoppedWithItsReasonAndNoAnswer(t *testing.T) {
	row := decisionRow("in-terminal", "request", 1)
	row.Attention.Reason = "Waiting in its terminal"
	w := &decisionWorld{rows: []dashboardSession{row}, failures: map[string]error{"in-terminal": actionFailed(400, "invalid_request", "this session's requests are not answered through Agenthail")}}
	page := w.page()
	if len(page.Decisions) != 1 || page.Decisions[0].Answerable || page.Decisions[0].Reason != "Waiting in its terminal" || page.Decisions[0].Group != decisionStopped {
		t.Fatalf("decisions=%+v", page.Decisions)
	}
}

func TestHeldAndFailedDeliveriesAreStoppedAndNeverReadAsQuestions(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("held", "incoming", 1), decisionRow("undelivered", "outgoing", 2), decisionRow("refused", "stopped", 3)}}
	page := w.page()
	if len(w.asked) != 0 {
		t.Fatalf("delivery and stop items asked a provider for requests: %v", w.asked)
	}
	for _, item := range page.Decisions {
		if item.Group != decisionStopped || item.Answerable {
			t.Fatalf("item=%+v", item)
		}
	}
	if len(page.Decisions) != 3 {
		t.Fatalf("decisions=%+v", page.Decisions)
	}
}

func TestRemoteAndReplacedLeadRowsAreNotDecisions(t *testing.T) {
	replaced := decisionRow("replaced", "request", 1)
	replaced.Attention.Kind = AttentionReplacedRequest
	remote := decisionRow("other-host::far", "request", 2)
	w := &decisionWorld{rows: []dashboardSession{replaced, remote}, open: map[string][]openRequestView{"other-host::far": {prompt("x")}}}
	if page := w.page(); len(page.Decisions) != 0 || len(w.asked) != 0 {
		t.Fatalf("decisions=%+v asked=%v", page.Decisions, w.asked)
	}
}

func TestAWorkersPromptHeldByALiveLeadIsLeadHandlingNotADecision(t *testing.T) {
	worker := decisionRow("worker", "request", 1)
	w := &decisionWorld{rows: []dashboardSession{worker, decisionRow("lead", "", 0)}, hearers: map[string]decisionHearer{"worker": {sessionID: "lead", role: "lead"}}, open: map[string][]openRequestView{"worker": {prompt("w")}}}
	page := w.page()
	if len(page.Decisions) != 0 || len(page.LeadHandling) != 1 || page.LeadHandling[0].HeldBy != "lead" || page.LeadHandling[0].Role != "lead" || page.LeadHandling[0].ProjectID != "web-app" {
		t.Fatalf("page=%+v", page)
	}
	if len(w.asked) != 0 {
		t.Fatalf("a lead-handled prompt asked its provider: %v", w.asked)
	}
}

func TestAWorkersPromptIsTheirsOnceTheLeadCannotHoldIt(t *testing.T) {
	cases := map[string]func(*dashboardSession){
		"lead archived":    func(lead *dashboardSession) { lead.Archived = true },
		"lead unavailable": func(lead *dashboardSession) { lead.Display = &SessionDisplay{Kind: "unavailable"} },
		"lead asks the person": func(lead *dashboardSession) {
			lead.Attention = &SessionAttention{Kind: AttentionAsk, Reason: "which layout"}
		},
		"lead waits on a prompt": func(lead *dashboardSession) { *lead = decisionRow("lead", "request", 0) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			lead := decisionRow("lead", "", 0)
			change(&lead)
			w := &decisionWorld{rows: []dashboardSession{decisionRow("worker", "request", 1), lead}, hearers: map[string]decisionHearer{"worker": {sessionID: "lead", role: "lead"}}, open: map[string][]openRequestView{"worker": {prompt("w")}, "lead": {prompt("l")}}}
			page := w.page()
			if groupsOf(page)["worker"] != decisionWaiting || len(page.LeadHandling) != 0 {
				t.Fatalf("page=%+v", page)
			}
		})
	}
}

func TestAPromptWithNoHearerOrAHearerNotListedIsADecision(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("alone", "request", 1), decisionRow("orphaned", "request", 2)}, hearers: map[string]decisionHearer{"orphaned": {sessionID: "missing-lead", role: "lead"}}, open: map[string][]openRequestView{"alone": {prompt("a")}, "orphaned": {prompt("b")}}}
	if groups := groupsOf(w.page()); groups["alone"] != decisionWaiting || groups["orphaned"] != decisionWaiting {
		t.Fatalf("groups=%v", groups)
	}
}

func TestAnEndedTurnsAskIsNeverLeadHandled(t *testing.T) {
	w := &decisionWorld{rows: []dashboardSession{decisionRow("asker", AttentionAsk, 1), decisionRow("lead", "", 0)}, hearers: map[string]decisionHearer{"asker": {sessionID: "lead", role: "lead"}}, open: map[string][]openRequestView{"asker": {prompt("ask")}}}
	page := w.page()
	if groupsOf(page)["asker"] != decisionWaiting || len(page.LeadHandling) != 0 {
		t.Fatalf("page=%+v", page)
	}
}
