package registry

import (
	"testing"
	"time"
)

func TestOpenTurnsEachStandingNeedsYouEndIntoAnOpenAsk(t *testing.T) {
	r := coordinatorRegistry(t, "asker", "worker", "settled", "lead")
	ended := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, step := range []string{
		`ALTER TABLE turn_ends RENAME TO turn_ends_new`,
		`DROP TABLE turn_ends_new`,
		`CREATE TABLE turn_ends (session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, started_at_ms INTEGER NOT NULL, ended_at_ms INTEGER NOT NULL DEFAULT 0,
			turn_id TEXT NOT NULL DEFAULT '', completed_turn_id TEXT NOT NULL DEFAULT '', needs TEXT NOT NULL DEFAULT '', headline TEXT NOT NULL DEFAULT '',
			asks_json TEXT NOT NULL DEFAULT 'null', next_json TEXT NOT NULL DEFAULT 'null', source TEXT NOT NULL DEFAULT '', routed_to TEXT NOT NULL DEFAULT '',
			answered_at_ms INTEGER NOT NULL DEFAULT 0, answer_status TEXT NOT NULL DEFAULT '', answer_message_id TEXT NOT NULL DEFAULT '', PRIMARY KEY (session_id, started_at_ms))`,
		`DELETE FROM asks`,
		`PRAGMA user_version=89`,
	} {
		if _, err := r.db.Exec(step); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	asks := `[{"question":"Deploy it now?","options":[{"label":"Deploy"},{"label":"Wait","does":"hold until Monday"}],"suggested":1,"links":[{"title":"Runbook","url":"https://example.com"}]}]`
	for _, end := range []struct{ session, needs, routed string }{{"asker", "you", ""}, {"worker", "you", "lead"}, {"settled", "you", ""}} {
		answered := int64(0)
		if end.session == "settled" {
			answered = ended + 1
		}
		if _, err := r.db.Exec(`INSERT INTO turn_ends(session_id,started_at_ms,ended_at_ms,needs,headline,asks_json,routed_to,answered_at_ms,source) VALUES(?,?,?,?,?,?,?,?,'tool')`,
			end.session, ended-1000, ended, end.needs, "Ready to ship", asks, end.routed, answered); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.migrate(true); err != nil {
		t.Fatal(err)
	}

	open, err := r.OpenAsks()
	if err != nil || len(open) != 2 {
		t.Fatalf("open asks %+v err %v, want the asker's and the worker's", open, err)
	}
	routed := map[string]Ask{}
	for _, ask := range open {
		routed[ask.SessionID] = ask
	}
	asker, worker := routed["asker"], routed["worker"]
	question := asker.Questions[0]
	if asker.RoutedTo != AskToPerson || asker.Title != "Ready to ship" || question.Question != "Deploy it now?" || len(question.Options) != 2 ||
		question.Options[1].Does != "hold until Monday" || question.Recommended != question.Options[1].ID || len(question.Links) != 1 || !asker.CreatedAt.Equal(time.UnixMilli(ended).UTC()) {
		t.Fatalf("the person's ask %+v", asker)
	}
	if worker.RoutedTo != "lead" {
		t.Fatalf("the ask told to the lead is routed to %q", worker.RoutedTo)
	}
	if end, found, err := r.CurrentTurnEnd("settled"); err != nil || !found || end.Headline != "Ready to ship" {
		t.Fatalf("the turn end itself was lost: %+v found=%v err %v", end, found, err)
	}
	for _, column := range []string{"needs", "asks_json", "routed_to", "answered_at_ms", "answer_status", "answer_message_id"} {
		var n int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('turn_ends') WHERE name=?`, column).Scan(&n); err != nil || n != 0 {
			t.Fatalf("turn_ends.%s is still there: %d %v", column, n, err)
		}
	}
}

func TestAnAskIsAnsweredOnceAndMovesOnlyWhileOpen(t *testing.T) {
	r := coordinatorRegistry(t, "worker", "lead")
	now := time.Now()
	ask, err := r.RaiseAsk(Ask{SessionID: "worker", Origin: AskOriginAgent, Title: "Ship?", Questions: []AskQuestion{{Question: "Ship?", Options: []AskOption{{ID: "o0", Label: "Yes"}}}}, RoutedTo: "lead"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := r.RouteAsk(ask.ID, AskToPerson, AskEscalated, now.Add(time.Minute)); err != nil || !moved {
		t.Fatalf("escalate: moved=%v err %v", moved, err)
	}
	if moved, err := r.RouteAsk(ask.ID, AskToPerson, AskEscalated, now.Add(2*time.Minute)); err != nil || moved {
		t.Fatalf("escalating to where it already is moved it again: %v %v", moved, err)
	}
	if claimed, err := r.ClaimAsk(ask.ID, AskAnswer{Answers: map[string]string{"Ship?": "Yes"}}, AskToPerson, "iphone", now.Add(3*time.Minute)); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if claimed, err := r.ClaimAsk(ask.ID, AskAnswer{Note: "again"}, "lead", "mcp", now.Add(4*time.Minute)); err != nil || claimed {
		t.Fatalf("a second answer was taken: %v %v", claimed, err)
	}
	if moved, err := r.RouteAsk(ask.ID, "lead", AskEscalated, now.Add(5*time.Minute)); err != nil || moved {
		t.Fatalf("an answered ask moved: %v %v", moved, err)
	}
	got, _, err := r.Ask(ask.ID)
	if err != nil || got.State != AskAnswered || got.Answer.Answers["Ship?"] != "Yes" || got.AnsweredVia != "iphone" || len(got.Route) != 2 || got.Route[1].Why != AskEscalated {
		t.Fatalf("ask %+v err %v", got, err)
	}
}

func TestARowShowsItsOldestOpenAskThenItsAnswerUntilTheNextTurn(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	now := time.Now()
	raise := func(title string, at time.Time) Ask {
		ask, err := r.RaiseAsk(Ask{SessionID: "worker", Origin: AskOriginAgent, Title: title, Questions: []AskQuestion{{Question: title, Options: []AskOption{{ID: "o0", Label: "Yes"}}}}, RoutedTo: AskToPerson}, at)
		if err != nil {
			t.Fatal(err)
		}
		return ask
	}
	first, second := raise("first", now), raise("second", now.Add(time.Second))
	shown := func() string {
		ask, found, err := r.RowAsk("worker")
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return ""
		}
		return ask.Title + ":" + ask.State
	}
	if got := shown(); got != "first:open" {
		t.Fatalf("row ask %q, want the oldest open one", got)
	}
	if _, err := r.ClaimAsk(first.ID, AskAnswer{Note: "yes"}, AskToPerson, "mac", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := shown(); got != "second:open" {
		t.Fatalf("row ask %q, want the open one before the answered one", got)
	}
	if _, err := r.ClaimAsk(second.ID, AskAnswer{Note: "yes"}, AskToPerson, "mac", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := shown(); got != "second:answered" {
		t.Fatalf("row ask %q, want the newest answer", got)
	}
	if err := r.OpenTurn("worker", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := shown(); got != "" {
		t.Fatalf("row ask %q after the next turn started", got)
	}
}
