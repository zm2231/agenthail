package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func waitAsk(defaultIndex, minutes int) map[string]any {
	return map[string]any{"title": "Rename the columns?", "default": defaultIndex, "defaultAfterMinutes": minutes, "questions": []any{
		map[string]any{"question": "Rename to snake_case?", "options": []any{map[string]any{"label": "Rename", "does": "rename every column"}, map[string]any{"label": "Keep", "does": "keep the names as they are"}}},
	}}
}

func TestAnAskWithADefaultTakesItAtItsDeadlineAndTellsTheSessionOnce(t *testing.T) {
	f := newMCPAnswerFixture(t)
	key, held := f.hold(t, "lead", "Import the March export", false)
	out, refused := callMCP(t, f.lead, "agenthail_ask", waitAsk(1, 15))
	if refused || out["workItem"] != held || out["defaultAt"] == nil {
		t.Fatalf("an ask with a default = %v", out)
	}
	askID := out["askId"].(string)
	f.syncQuestions(t)
	if questions := f.itemQuestions(t, key, held); len(questions) != 1 || questions[0].Default == nil || *questions[0].Default != 1 || questions[0].DeadlineMs == 0 {
		t.Fatalf("the item's question does not carry the default: %+v", questions)
	}

	f.d.sweepAsks(time.Now().Add(14 * time.Minute))
	if ask, _, _ := f.r.Ask(askID); ask.State != registry.AskOpen {
		t.Fatalf("the default applied before its deadline: %s", ask.State)
	}
	if expired, err := f.r.ApplyAskDefaults(time.Now().Add(16 * time.Minute)); err != nil || len(expired) != 1 {
		t.Fatalf("the default did not apply at its deadline: %+v %v", expired, err)
	}
	if got := noticesTo(t, f.r, "lead", "the default applies"); got != 0 {
		t.Fatal("the session was told before any sweep")
	}
	for range 2 {
		f.d.sweepAsks(time.Now().Add(16 * time.Minute))
	}
	if ask, _, _ := f.r.Ask(askID); ask.State != registry.AskExpired {
		t.Fatalf("after the deadline the ask is %s", ask.State)
	}
	if got := noticesTo(t, f.r, "lead", "; the default applies: Keep."); got != 1 {
		t.Fatalf("the session was told %d times that the default applies", got)
	}
	f.syncQuestions(t)
	questions := f.itemQuestions(t, key, held)
	if len(questions) != 1 || questions[0].Outcome != ledger.ItemQuestionDefaulted || !strings.Contains(questions[0].Answer, "the default applies: Keep") {
		t.Fatalf("the item's question after the deadline: %+v", questions)
	}
	if lanes, _ := f.lanes(t); lanes[held] == laneYou {
		t.Fatal("an ask that took its default keeps its item waiting on the person")
	}
}

func TestAnAskAnsweredBeforeItsDeadlineNeverTakesItsDefault(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.hold(t, "lead", "Import the March export", false)
	out, refused := callMCP(t, f.lead, "agenthail_ask", waitAsk(1, 15))
	if refused {
		t.Fatalf("ask = %v", out)
	}
	askID := out["askId"].(string)
	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
	if reply := f.d.doAction(t.Context(), person, dashboardActionRequest{Action: "respond", SessionID: "lead", RequestID: askID, Decision: surface.Response{Choices: []string{"o0"}}}); reply.Status != 200 {
		t.Fatalf("answer status=%d %s", reply.Status, reply.Body)
	}
	f.d.sweepAsks(time.Now().Add(16 * time.Minute))
	if ask, _, _ := f.r.Ask(askID); ask.State != registry.AskAnswered {
		t.Fatalf("an answered ask is %s after its deadline", ask.State)
	}
	if got := noticesTo(t, f.r, "lead", "the default applies"); got != 0 {
		t.Fatal("the session was told a default applies to an answered ask")
	}
}

func TestAnAskRefusesADefaultItCannotSafelyTake(t *testing.T) {
	f := newMCPAnswerFixture(t)
	outward := waitAsk(0, 15)
	outward["questions"] = []any{map[string]any{"question": "Ready?", "options": []any{map[string]any{"label": "Deploy", "does": "ship it to production"}, map[string]any{"label": "Hold"}}}}
	withoutMinutes := waitAsk(1, 0)
	twoQuestions := waitAsk(1, 15)
	twoQuestions["questions"] = append(twoQuestions["questions"].([]any), map[string]any{"question": "Which first?", "options": []any{map[string]any{"label": "a"}, map[string]any{"label": "b"}}})
	for name, c := range map[string]struct {
		input map[string]any
		says  string
	}{
		"an outward default":          {outward, "an outward default; leave default out so the person answers"},
		"a default without minutes":   {withoutMinutes, "give both or neither"},
		"a default under 15 minutes":  {waitAsk(1, 14), "15 to 10080"},
		"a default over a week":       {waitAsk(1, 10081), "15 to 10080"},
		"a default that is no option": {waitAsk(2, 15), "option index from 0 to 1"},
		"a default on two questions":  {twoQuestions, "only an ask with one question"},
	} {
		if out, refused := callMCP(t, f.lead, "agenthail_ask", c.input); !refused || !strings.Contains(out["message"].(string), c.says) {
			t.Errorf("%s = %v", name, out)
		}
	}
}
