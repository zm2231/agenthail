package daemon

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestThePersonsQuestionOnAWorkItemWaitsOnThemOnThatItemAlone(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, asked := f.hold(t, "lead", "Import the March export", false)
	_, other := f.hold(t, "lead", "Rename the export columns", false)
	question := func(to string) (ledger.Question, error) {
		return f.d.ledgerStore().AskQuestion(ledger.QuestionRequest{ProjectKey: key, SessionID: "lead", To: to, Text: "Import March only, or the whole quarter?",
			Recommendation: "March only", DefaultAnswer: "March only", DeadlineMs: time.Now().Add(4 * time.Hour).UnixMilli(), Kind: ledger.QuestionScope}, time.Now())
	}
	q, err := question(asked)
	if err != nil {
		t.Fatalf("a question to a work item: %v", err)
	}
	f.d.syncLedgerAsks(key)

	open, err := f.r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(open, func(ask registry.Ask) bool { return ask.Origin == registry.AskOriginLedger })
	if i < 0 || open[i].WorkItem != asked || open[i].LedgerKey != key || open[i].RoutedTo != registry.AskToPerson {
		t.Fatalf("the person's ask for %s = %+v", q.ID, open)
	}
	lanes, flights := f.lanes(t)
	if lanes[asked] != laneYou || !slices.Contains(flights[asked].WaitsOn, "person") || lanes[other] == laneYou {
		t.Fatalf("lanes = %v", lanes)
	}
	page := freshDecisions(t, f.d)
	if j := slices.IndexFunc(page.Decisions, func(item decisionItem) bool { return item.Key == "ask:"+open[i].ID }); j < 0 ||
		page.Decisions[j].WorkItem != asked || page.Decisions[j].WorkTitle != "Import the March export" {
		t.Fatalf("Needs you does not name the question's item: %+v", page.Decisions)
	}
	f.syncQuestions(t)
	if questions := f.itemQuestions(t, key, asked); len(questions) != 0 {
		t.Fatalf("the person's own question was recorded as an agent's ask on the item: %+v", questions)
	}

	if _, err := f.d.ledgerStore().Work(ledger.WorkRequest{ProjectKey: key, SessionID: "lead", Action: ledger.WorkDelete, ItemID: other}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := question(other); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("a question to a deleted item: %v", err)
	}
	if _, err := question("widget-W404"); err == nil || !strings.Contains(err.Error(), "work item") {
		t.Fatalf("a question to nothing: %v", err)
	}
}
