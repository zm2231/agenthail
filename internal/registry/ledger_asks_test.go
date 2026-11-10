package registry

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func ledgerAsks(t *testing.T, r *Registry) []Ask {
	t.Helper()
	asks, err := queryAsks(t.Context(), r.read, `WHERE origin=? ORDER BY ref`, AskOriginLedger)
	if err != nil {
		t.Fatal(err)
	}
	return asks
}

func TestAnOpenQuestionFromALocalSessionAsksThePersonOnce(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "asker", "mirrored")
	if _, err := r.db.Exec(`UPDATE sessions SET host_id='studio' WHERE id='mirrored'`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour).Truncate(time.Millisecond).UTC()
	questions := []LedgerQuestion{
		{ID: "Q1", OwnerSessionID: "asker", Text: "Index titles only, or bodies too?", Recommendation: "Bodies too", DefaultAnswer: "Titles only", Deadline: deadline},
		{ID: "Q2", OwnerSessionID: "asker", Text: "Publish the post?", Recommendation: "don't", DefaultAnswer: "don't", Deadline: deadline},
		{ID: "Q3", OwnerSessionID: "mirrored", Text: "Asked on another host", Recommendation: "yes", DefaultAnswer: "no", Deadline: deadline},
	}
	for range 2 {
		if _, err := r.SyncLedgerAsks("github.com/acme/app", questions, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	asks := ledgerAsks(t, r)
	if len(asks) != 2 {
		t.Fatalf("asks=%+v, want one per local question however often it syncs", asks)
	}
	first := asks[0]
	if first.SessionID != "asker" || first.RoutedTo != AskToPerson || first.Ref != "ledger:github.com/acme/app:Q1" || !first.Deadline.Equal(deadline) || !strings.HasPrefix(first.Title, "Q1: ") {
		t.Fatalf("ask=%+v", first)
	}
	question := first.Questions[0]
	if question.Recommended != LedgerRecommended || len(question.Options) != 2 || question.Options[0].Label != "Bodies too" || question.Options[1].ID != LedgerDefault || question.Options[1].Label != "Titles only" {
		t.Fatalf("question=%+v, want the recommendation then the default", question)
	}
	if options := asks[1].Questions[0].Options; len(options) != 1 || options[0].ID != LedgerRecommended {
		t.Fatalf("options=%+v, want one option when the default is the recommendation", options)
	}
	if untold, err := r.UntoldLedgerAsks(); err != nil || len(untold) != 2 {
		t.Fatalf("untold=%+v err=%v", untold, err)
	}
}

func TestAQuestionAnsweredElsewhereWithdrawsItsAskAndADefaultedOneExpires(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "asker")
	deadline := time.Now().Add(time.Hour)
	questions := []LedgerQuestion{
		{ID: "Q1", OwnerSessionID: "asker", Text: "Which index?", Recommendation: "Bodies", DefaultAnswer: "Titles", Deadline: deadline},
		{ID: "Q2", OwnerSessionID: "asker", Text: "Which cache?", Recommendation: "Redis", DefaultAnswer: "Memory", Deadline: deadline},
	}
	if _, err := r.SyncLedgerAsks("p", questions, time.Now()); err != nil {
		t.Fatal(err)
	}
	questions[0].Answered, questions[1].Defaulted = true, true
	expired, err := r.SyncLedgerAsks("p", questions, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Ref != "ledger:p:Q2" || expired[0].State != AskExpired {
		t.Fatalf("expired=%+v, want the defaulted question's ask", expired)
	}
	states := map[string]string{}
	for _, ask := range ledgerAsks(t, r) {
		states[ask.Ref] = ask.State
	}
	if states["ledger:p:Q1"] != AskWithdrawn || states["ledger:p:Q2"] != AskExpired {
		t.Fatalf("states=%v", states)
	}
	if again, err := r.SyncLedgerAsks("p", questions, time.Now()); err != nil || len(again) != 0 {
		t.Fatalf("again=%+v err=%v, want an expiry reported once", again, err)
	}
	if asks := ledgerAsks(t, r); len(asks) != 2 {
		t.Fatalf("asks=%+v, a closed question must not ask again", asks)
	}
}

func TestParseLedgerAskRefKeepsColonsInTheProjectKey(t *testing.T) {
	key, id, ok := ParseLedgerAskRef(LedgerAskRef("host::github.com/acme/app", "Q12"))
	if !ok || key != "host::github.com/acme/app" || id != "Q12" {
		t.Fatalf("key=%q id=%q ok=%v", key, id, ok)
	}
	for _, ref := range []string{"intent:1:0:failed", "ledger:", "ledger:p:", "ledger::Q1"} {
		if _, _, ok := ParseLedgerAskRef(ref); ok {
			t.Fatalf("%q parsed as a ledger ref", ref)
		}
	}
}

func TestARegistryAtSchema92KeepsItsAsksAndCanExpireOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "asker")
	if _, err := r.RaiseAsk(Ask{SessionID: "asker", Origin: AskOriginAgent, Title: "Ship it?", RoutedTo: AskToPerson, Questions: []AskQuestion{{Question: "Ship it?", Options: []AskOption{{ID: "o0", Label: "Ship"}}}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, r)
	const columns = `id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,answer_json,answered_by,answered_via,answered_at_ms,applied_at_ms,apply_status,apply_id,created_at_ms,ref,wait_kind,answerable,announced_at_ms`
	for _, statement := range []string{
		`ALTER TABLE asks RENAME TO asks_93`,
		`CREATE TABLE asks (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			origin TEXT NOT NULL,
			title TEXT NOT NULL,
			questions_json TEXT NOT NULL,
			routed_to TEXT NOT NULL,
			routed_at_ms INTEGER NOT NULL,
			route_json TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('open','answered','withdrawn')),
			answer_json TEXT NOT NULL DEFAULT '',
			answered_by TEXT NOT NULL DEFAULT '',
			answered_via TEXT NOT NULL DEFAULT '',
			answered_at_ms INTEGER NOT NULL DEFAULT 0,
			applied_at_ms INTEGER NOT NULL DEFAULT 0,
			apply_status TEXT NOT NULL DEFAULT '',
			apply_id TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			ref TEXT NOT NULL DEFAULT '',
			wait_kind TEXT NOT NULL DEFAULT '',
			answerable INTEGER NOT NULL DEFAULT 1,
			announced_at_ms INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO asks (` + columns + `) SELECT ` + columns + ` FROM asks_93`,
		`DROP TABLE asks_93`,
		`CREATE INDEX asks_open ON asks(routed_to) WHERE state='open'`,
		`CREATE INDEX asks_session ON asks(session_id, created_at_ms)`,
		`CREATE UNIQUE INDEX asks_ref ON asks(session_id, origin, ref) WHERE ref<>'' AND state<>'withdrawn'`,
		`PRAGMA user_version = 92`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	open, err := r.OpenAsks()
	if err != nil || len(open) != 1 || open[0].Title != "Ship it?" {
		t.Fatalf("open=%+v err=%v, want the ask kept", open, err)
	}
	if _, err := r.SyncLedgerAsks("p", []LedgerQuestion{{ID: "Q1", OwnerSessionID: "asker", Text: "Which?", Recommendation: "A", DefaultAnswer: "B", Deadline: time.Now().Add(time.Hour)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	expired, err := r.SyncLedgerAsks("p", []LedgerQuestion{{ID: "Q1", OwnerSessionID: "asker", Defaulted: true}}, time.Now())
	if err != nil || len(expired) != 1 {
		t.Fatalf("expired=%+v err=%v, want the migrated table to take an expired ask", expired, err)
	}
	indexes, err := queryRows(r.read, nil, scanString, `SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='asks'`)
	if err != nil || !slices.Contains(indexes, "asks_ref") || !slices.Contains(indexes, "asks_open") {
		t.Fatalf("indexes=%v err=%v", indexes, err)
	}
}
