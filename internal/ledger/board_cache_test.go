package ledger_test

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func snapshot(t *testing.T, b *ledger.Board) string {
	t.Helper()
	goals, asks, decisions, questions, items := b.Goals(), b.AllAsks(), b.Decisions(), b.Questions(), b.WorkItems()
	sort.Slice(goals, func(i, j int) bool { return goals[i].ID < goals[j].ID })
	sort.Slice(asks, func(i, j int) bool { return asks[i].ID < asks[j].ID })
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID < decisions[j].ID })
	sort.Slice(questions, func(i, j int) bool { return questions[i].ID < questions[j].ID })
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.Marshal(map[string]any{
		"goals": goals, "asks": asks, "decisions": decisions, "questions": questions, "work": items,
		"utterances": b.Utterances(), "targetCuts": b.TargetCuts(), "constraints": b.ProjectConstraints(),
		"waivers": b.G2Waivers(), "applied": b.AppliedSeq(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheCachedBoardEqualsABoardRebuiltFromTheLog(t *testing.T) {
	db := openTestDB(t)
	cached := ledger.NewCachedStore(db, db)
	g := searchAsk(t, cached)
	if _, err := cached.Board(project); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,actor_host_id,owner_seq,ref,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		project, string(ledger.EventGoal), `{"goalId":"widget-G9","name":"never committed"}`, "s1", "", 0, "", time.Now().UTC().Format("2006-01-02T15:04:05Z")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	capture(t, cached, project, "claude-prompt:2", "and export")
	attach(t, cached, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"export results"}})
	if _, err := cached.AskQuestion(ledger.QuestionRequest{ProjectKey: project, SessionID: "s1", To: g, Text: "which format?", Recommendation: "csv", DefaultAnswer: "csv and json", KeepsBreadth: true, Kind: ledger.QuestionScope, DeadlineMs: time.Now().Add(time.Minute).UnixMilli()}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.ExpireQuestions(project, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	work(t, cached, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C2", Text: "csv writer"})
	clauseAction(t, cached, ledger.ClauseRequest{Action: ledger.ClauseProgress, ClauseID: "widget-C1", Ref: "commit abc"}, quiet)
	gate(t, cached, "t1", quiet)
	if _, err := cached.Attach(ledger.AttachRequest{ProjectKey: project, SessionID: "s1", UtteranceID: "claude-prompt:404", As: ledger.AsConversation}); err == nil {
		t.Fatal("a refused attach was accepted")
	}
	incremental, err := cached.Board(project)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := ledger.NewStore(db, db).Board(project)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snapshot(t, incremental), snapshot(t, rebuilt); got != want {
		t.Fatalf("incremental board differs from the rebuilt one:\n got %s\nwant %s", got, want)
	}
	for _, goal := range incremental.Goals() {
		if goal.ID == "widget-G9" {
			t.Fatal("a rolled-back write reached the cache")
		}
	}
}
