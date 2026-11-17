package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type judgeCall struct {
	model     string
	state     map[string]string
	questions []string
}

type judgeStub struct {
	mu         sync.Mutex
	down       bool
	secondDown bool
	calls      []judgeCall
}

func (s *judgeStub) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string                     `json:"model"`
		State     map[string]string          `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	call := judgeCall{model: body.Model, state: body.State}
	for name := range body.Questions {
		call.questions = append(call.questions, name)
	}
	sort.Strings(call.questions)
	s.mu.Lock()
	s.calls = append(s.calls, call)
	_, promise := body.Questions["promise"]
	down := s.down || s.secondDown && !promise
	s.mu.Unlock()
	if down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if promise {
		io.WriteString(w, `{"answers":{"promise":{"type":"noul","noul":0.62}}}`)
		return
	}
	io.WriteString(w, `{"answers":{"needs":{"type":"noul","noul":0.08},"needs_tail":{"type":"noul","noul":0.05},
		"state":{"choice":"continuing","confidence":0.7,"probabilities":{"done":0.1,"waiting_person":0.1,"waiting_work":0.1,"continuing":0.7}}}}`)
}

func (s *judgeStub) asked() []judgeCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]judgeCall(nil), s.calls...)
}

func judgeFixture(t *testing.T, stub *judgeStub, extra string) (*Daemon, *registry.Registry, *daemonSurface, surface.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	t.Cleanup(server.Close)
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(fmt.Sprintf(`{"turnJudge": {"url": %q, "model": "d1-3b"%s}}`, server.URL, extra)), 0o600); err != nil {
		t.Fatal(err)
	}
	d, r, fake, from, _ := daemonFixture(t)
	d.judge.http = server.Client()
	d.startTurnJudge(t.Context())
	return d, r, fake, from
}

func finishJudgedTurn(d *Daemon, fake *daemonSurface, session *surface.Session, turnID, reply string, background *surface.BackgroundTasks) {
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusIdle},
		{Status: surface.StatusBusy, ActiveTurnID: turnID},
		{Status: surface.StatusIdle, CompletedTurnID: turnID, Reply: &surface.ReplyResult{Text: reply, Done: true}, BackgroundTasks: background},
	} {
		fake.observations[session.ID] = observation
		d.observeSession(d.background.ctx, fake, session)
	}
}

func readJudgments(t *testing.T, d *Daemon, sessionID string) turnJudgmentsPage {
	t.Helper()
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: dashboardTestToken}
	var page turnJudgmentsPage
	if code := api.do(http.MethodGet, "/api/v1/turn-judgments?sessionId="+sessionID, "", &page); code != http.StatusOK {
		t.Fatalf("turn judgments answered %d", code)
	}
	return page
}

func TestAFinishedTurnIsJudgedOnItsEndingWithWhatItWaitsOn(t *testing.T) {
	stub := &judgeStub{}
	d, r, fake, from := judgeFixture(t, stub, "")
	if _, err := r.RaiseAsk(registry.Ask{SessionID: from.ID, Origin: registry.AskOriginAgent, Title: "Ship?", Questions: []registry.AskQuestion{{Question: "Ship?", Options: []registry.AskOption{{ID: "o0", Label: "Yes"}}}}, RoutedTo: registry.AskToPerson}, time.Now()); err != nil {
		t.Fatal(err)
	}
	reply := "I fixed the parser.\n\n```go\nfmt.Println(\"done.\")\n```\n\nThe tests pass. Next:\n- I'll trace the flaky test now."
	background := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceHost, Tasks: []surface.BackgroundTask{{ID: "b1", Kind: surface.BackgroundShell}}}
	finishJudgedTurn(d, fake, &from, "turn-1", reply, background)
	eventually(t, "the judgment", func() bool {
		return len(readJudgments(t, d, from.ID).Judgments) == 1 && !readJudgments(t, d, from.ID).Judgments[0].JudgedAt.IsZero()
	})
	d.observeSession(t.Context(), fake, &from)

	page := readJudgments(t, d, from.ID)
	j := page.Judgments[0]
	if j.TurnID != "turn-1" || j.Model != "d1-3b" || j.PromiseModel != "d1-3b" || j.Promise != 0.62 || j.Needs != 0.08 || j.NeedsTail != 0.05 || j.State != "continuing" || j.StateP["continuing"] != 0.7 || j.Error != "" {
		t.Fatalf("judgment = %+v", j)
	}
	if want := (registry.TurnFacts{BackgroundRunning: true, BackgroundSource: surface.BackgroundSourceHost, BackgroundTasks: 1, OpenAsks: 1, AskedOf: []string{registry.AskToPerson}}); !reflect.DeepEqual(j.Facts, want) {
		t.Fatalf("facts = %+v, want %+v", j.Facts, want)
	}
	ending := "Next: - I'll trace the flaky test now."
	want := []judgeCall{
		{model: "d1-3b", state: map[string]string{"reply": ending}, questions: []string{"promise"}},
		{model: "d1-3b", state: map[string]string{"reply": ending, "full_reply": reply}, questions: []string{"needs", "needs_tail", "state"}},
	}
	if asked := stub.asked(); !reflect.DeepEqual(asked, want) {
		t.Fatalf("the judge was asked %+v, want the promise on the ending alone, then the rest with the whole reply", asked)
	}
	if j.Ending != ending || page.Judge == nil || page.Judge.Model != "d1-3b" || page.Judge.Failures != 0 {
		t.Fatalf("ending %q, judge %+v", j.Ending, page.Judge)
	}
}

func TestATurnTheJudgeCannotAnswerIsStillRecordedWithItsFacts(t *testing.T) {
	stub := &judgeStub{down: true}
	d, _, fake, from := judgeFixture(t, stub, "")
	finishJudgedTurn(d, fake, &from, "turn-1", "Done. I'll push it next.", nil)
	eventually(t, "the failed judgment", func() bool {
		page := readJudgments(t, d, from.ID)
		return len(page.Judgments) == 1 && page.Judgments[0].Error != "" && page.Judge != nil && page.Judge.Failures == 1
	})
}

func TestAJudgmentWhoseSecondCallFailsKeepsNoAnswers(t *testing.T) {
	stub := &judgeStub{secondDown: true}
	d, _, fake, from := judgeFixture(t, stub, "")
	finishJudgedTurn(d, fake, &from, "turn-1", "Done. I'll push it next.", nil)
	eventually(t, "the failed judgment", func() bool {
		page := readJudgments(t, d, from.ID)
		return len(page.Judgments) == 1 && page.Judgments[0].Error != "" && page.Judge != nil && page.Judge.Failures == 1
	})
	if j := readJudgments(t, d, from.ID).Judgments[0]; j.Promise != 0 || j.State != "" {
		t.Fatalf("a judgment whose second call failed kept answers: %+v", j)
	}
}

func TestThePromiseQuestionGoesToTheModelNamedForIt(t *testing.T) {
	stub := &judgeStub{}
	d, _, fake, from := judgeFixture(t, stub, `, "promiseModel": " d1-3b-promise "`)
	finishJudgedTurn(d, fake, &from, "turn-1", "The parser is fixed. I'm starting the review now.", nil)
	eventually(t, "the judgment", func() bool {
		page := readJudgments(t, d, from.ID)
		return len(page.Judgments) == 1 && !page.Judgments[0].JudgedAt.IsZero()
	})
	asked := stub.asked()
	if len(asked) != 2 || asked[0].model != "d1-3b-promise" || !reflect.DeepEqual(asked[0].questions, []string{"promise"}) || asked[1].model != "d1-3b" {
		t.Fatalf("the judge was asked %+v, want the promise of d1-3b-promise and the rest of d1-3b", asked)
	}
	page := readJudgments(t, d, from.ID)
	if j := page.Judgments[0]; j.Model != "d1-3b" || j.PromiseModel != "d1-3b-promise" || j.Promise != 0.62 || j.State != "continuing" {
		t.Fatalf("judgment = %+v", j)
	}
	if page.Judge == nil || page.Judge.Model != "d1-3b" || page.Judge.PromiseModel != "d1-3b-promise" {
		t.Fatalf("judge = %+v", page.Judge)
	}
}

func TestAPromiseModelThatNamesNoModelKeepsTheJudgeOffAndSaysWhy(t *testing.T) {
	for name, value := range map[string]string{"blank": `" "`, "not a string": `5`, "null": `null`} {
		t.Run(name, func(t *testing.T) {
			stub := &judgeStub{}
			d, _, fake, from := judgeFixture(t, stub, `, "promiseModel": `+value)
			finishJudgedTurn(d, fake, &from, "turn-1", "Done. I'll push it next.", nil)
			page := readJudgments(t, d, from.ID)
			if len(page.Judgments) != 0 || len(stub.asked()) != 0 || page.Judge == nil || !strings.Contains(page.Judge.Error, "promiseModel must name a model") {
				t.Fatalf("promiseModel %s judged %+v with judge %+v", value, page.Judgments, page.Judge)
			}
		})
	}
}

func TestNoTurnIsJudgedUntilTheJudgeIsConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, _, fake, from, _ := daemonFixture(t)
	d.startTurnJudge(t.Context())
	finishJudgedTurn(d, fake, &from, "turn-1", "Done. I'll push it next.", nil)
	if page := readJudgments(t, d, from.ID); len(page.Judgments) != 0 || page.Judge != nil {
		t.Fatalf("an unconfigured judge recorded %+v", page)
	}
}

func TestTheEndingLeavesOutCodeAndAClosingBlockOfLinks(t *testing.T) {
	for reply, want := range map[string]string{
		"Done.\n```go\nsecret := 1":                "Done.",
		"First. Second.\n```sh\nmake\n```\nThird.": "Second. Third.",
		"I pushed it. Next I'll rerun CI.\n\nSources:\n- [PR](https://example.com/1)\n- https://example.com/2": "I pushed it. Next I'll rerun CI.",
	} {
		if got := replyEnding(reply, 2); got != want {
			t.Fatalf("replyEnding(%q) = %q, want %q", reply, got, want)
		}
	}
}
