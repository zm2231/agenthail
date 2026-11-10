package cli

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLedgerBoardSaysWhatItCaptures(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: "board-key"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	board := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	for i := range 7 {
		u := ledger.Utterance{ProjectKey: "board-key", Ref: "claude-prompt:" + string(rune('a'+i)), SpeakerID: "person", TargetID: "lead", Text: "word " + string(rune('a'+i)), CapturedBy: ledger.CapturedByHook}
		if _, _, err := ls.RecordUtterance(u); err != nil {
			t.Fatal(err)
		}
	}
	out := board()
	if !strings.Contains(out, "to the project's lead; each message stays provisional until it is attached, and nothing waits on it") || strings.Contains(out, "G2:") {
		t.Fatalf("unset project board %q", out)
	}
	if !strings.Contains(out, "provisional until attached (7), newest 5; --provisional lists every one") || strings.Contains(out, "word b") || !strings.Contains(out, "word g") {
		t.Fatalf("an unset project lists more than the newest words %q", out)
	}
	if out := board("--all"); !strings.Contains(out, "word a") {
		t.Fatalf("--all leaves words out %q", out)
	}
	if out := board("--provisional"); !strings.HasPrefix(out, "provisional until attached (7):\n") || !strings.Contains(out, "word a") || strings.Contains(out, "capturing") {
		t.Fatalf("--provisional should list only every provisional message %q", out)
	}
	if err := ls.Enable("board-key", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if out := board(); !strings.Contains(out, "capturing since") || !strings.Contains(out, "not in the ledger") || !strings.Contains(out, "G2:") || !strings.Contains(out, "provisional until attached (7):") {
		t.Fatalf("enabled project board %q", out)
	}
	var enabled struct {
		Capture      string `json:"capture"`
		CaptureSince string `json:"captureSince"`
	}
	if err := json.Unmarshal([]byte(board("--json")), &enabled); err != nil || enabled.Capture != "enabled" || enabled.CaptureSince == "" {
		t.Fatalf("enabled board json %+v err %v", enabled, err)
	}
	if err := ls.Enable("board-key", ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	if out := board(); !strings.Contains(out, "sessions hear how many are provisional at checkpoints") || strings.Contains(out, "ledger attach") {
		t.Fatalf("checkpoint board %q", out)
	}
	var mode struct {
		Attach ledger.AttachMode `json:"attach"`
	}
	if err := json.Unmarshal([]byte(board("--json")), &mode); err != nil || mode.Attach != ledger.AttachAtCheckpoints {
		t.Fatalf("checkpoint board json %+v err %v", mode, err)
	}
	if err := ls.Disable("board-key"); err != nil {
		t.Fatal(err)
	}
	if out := board(); !strings.Contains(out, "disabled") {
		t.Fatalf("disabled project board %q", out)
	}
}

func TestLedgerBoardShowsGoalsProvenanceAndProvisionalWords(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const key, other = "repo:github.com/acme/widget", "repo:github.com/acme/infra"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	for _, u := range []ledger.Utterance{
		{ProjectKey: key, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: "add a cart", CapturedBy: ledger.CapturedByHook},
		{ProjectKey: key, Ref: "claude-prompt:2", SpeakerID: "person", TargetID: "s1", Text: "and fix the   deploy", CapturedBy: ledger.CapturedByHook},
		{ProjectKey: key, Ref: "claude-prompt:3", SpeakerID: "person", TargetID: "s1", Text: "rotate the keys", CapturedBy: ledger.CapturedByHook},
		{ProjectKey: other, Ref: "claude-prompt:3", SpeakerID: "person", TargetID: "s1", Text: "rotate the keys", CapturedBy: ledger.CapturedByHook},
	} {
		if _, _, err := ls.RecordUtterance(u); err != nil {
			t.Fatal(err)
		}
	}
	reply := ledger.NewReply("r1", "2026-11-10T10:00:00Z", "Both pipeline runs stopped at the same place: the deploy fails on the migration step. Should I fix it now?")
	if _, err := ls.RecordContext(key, ledger.ContextPayload{UtteranceID: "claude-prompt:2", Status: ledger.ReplyLate, JoinedBy: ledger.JoinedByTime, Reply: reply}, time.Now()); err != nil {
		t.Fatal(err)
	}
	g, _, _ := ls.EnsureGoal(key, "Checkout", "s1")
	og, _, _ := ls.EnsureGoal(other, "Keys", "s1")
	for _, req := range []ledger.AttachRequest{
		{ProjectKey: key, SessionID: "s1", UtteranceID: "claude-prompt:1", As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"add a cart"}},
		{ProjectKey: other, SessionID: "s1", UtteranceID: "claude-prompt:3", As: ledger.AsNewAsk, To: og.ID, Clauses: []string{"rotate the keys"}},
	} {
		if _, err := ls.Attach(req); err != nil {
			t.Fatal(err)
		}
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger(nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"widget-G1 Checkout  asks widget-A1",
		"widget-C1: add a cart  [captured, stated] owner=s1 from claude-prompt:1",
		"provisional until attached (1):\n  claude-prompt:2 to s1: and fix the deploy\n    answering, by time: ... the same place: the deploy fails on the migration step. Should I fix it now?\n",
		"claude-prompt:3 attached elsewhere: repo:github.com/acme/infra infra-G1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("board lacks %q:\n%s", want, out)
		}
	}
	goals, err := captureStdout(t, func() error { return app.cmdLedger([]string{"goals", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var parsed []struct {
		ID      string `json:"id"`
		Clauses []struct {
			Sources []string `json:"sourceUtteranceIds"`
		} `json:"clauses"`
	}
	if err := json.Unmarshal([]byte(goals), &parsed); err != nil || len(parsed) != 1 || len(parsed[0].Clauses) != 1 || parsed[0].Clauses[0].Sources[0] != "claude-prompt:1" {
		t.Fatalf("goals json %s err %v", goals, err)
	}
}

func TestLedgerBoardKeepsWithdrawnCutsAndMarksDefaultedDecisions(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const key = "repo:github.com/acme/widget"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	if _, _, err := ls.RecordUtterance(ledger.Utterance{ProjectKey: key, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: "keep it all", CapturedBy: ledger.CapturedByHook}); err != nil {
		t.Fatal(err)
	}
	ls.EnsureGoal(key, "Search", "s1")
	now := time.Now()
	for _, req := range []ledger.QuestionRequest{
		{ProjectKey: key, SessionID: "s1", To: "widget-G1", Text: "how broad?", Recommendation: "all", DefaultAnswer: "a smaller first version", Kind: ledger.QuestionScope, DeadlineMs: now.Add(time.Hour).UnixMilli()},
		{ProjectKey: key, SessionID: "s1", To: "widget-G1", Text: "which index?", Recommendation: "fts", DefaultAnswer: "fts and fuzzy", KeepsBreadth: true, Kind: ledger.QuestionScope, DeadlineMs: now.Add(time.Hour).UnixMilli()},
	} {
		if _, err := ls.AskQuestion(req, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ls.ExpireQuestions(key, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Attach(ledger.AttachRequest{ProjectKey: key, SessionID: "s1", UtteranceID: "claude-prompt:1", As: ledger.AsAnswers, To: "widget-Q1"}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger(nil) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"cut widget-X1 on widget-G1: default of widget-Q1 on expiry: a smaller first version (withdrawn: its question was answered)",
		"1 unanswered defaults became pending cuts",
		"applied by default from widget-Q2; not the person's decision, authorizes nothing outward",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("board lacks %q:\n%s", want, out)
		}
	}
}

func TestLedgerEnableAndDisableAskTheDaemon(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		answer := map[string]any{"projectKey": body["projectKey"]}
		if session, ok := body["sessionId"]; ok {
			answer["sessionId"], answer["granted"] = session, body["enabled"]
		} else {
			answer["enabled"], answer["attach"] = body["enabled"], body["attach"]
		}
		return http.StatusOK, answer
	})
	run := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	key := "repo:github.com/acme/widget"
	if out := run("enable", "--attach", "checkpoints"); !strings.Contains(out, "stay provisional until attached, and sessions see how many at checkpoints") || strings.Contains(out, "sessions attach") {
		t.Fatalf("enable output %q", out)
	}
	run("enable", "s1")
	run("disable", "s1")
	run("disable")
	want := []map[string]any{
		{"path": "/api/v1/ledger/scope", "projectKey": key, "enabled": true, "attach": "checkpoints"},
		{"path": "/api/v1/ledger/scope", "projectKey": key, "enabled": true, "sessionId": "s1"},
		{"path": "/api/v1/ledger/scope", "projectKey": key, "enabled": false, "sessionId": "s1"},
		{"path": "/api/v1/ledger/scope", "projectKey": key, "enabled": false},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("requests %v, want %v", *calls, want)
	}
	for _, args := range [][]string{{"enable", "--attach", "whenever"}, {"enable", "s1", "--attach", "checkpoints"}} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	if len(*calls) != len(want) {
		t.Fatalf("a refused enable reached the daemon: %v", *calls)
	}
	printed := map[string][]string{
		`{"attach":"checkpoints","enabled":true,"projectKey":"repo:github.com/acme/widget"}`: {"enable", "--attach", "checkpoints", "--json"},
		`{"granted":true,"projectKey":"repo:github.com/acme/widget","sessionId":"s1"}`:       {"enable", "s1", "--json"},
		`{"granted":false,"projectKey":"repo:github.com/acme/widget","sessionId":"s1"}`:      {"disable", "s1", "--json"},
		`{"enabled":false,"projectKey":"repo:github.com/acme/widget"}`:                       {"disable", "--json"},
	}
	for want, args := range printed {
		if out := run(args...); strings.Join(strings.Fields(out), "") != want {
			t.Errorf("%v printed %s, want %s", args, out, want)
		}
	}
	if out := run("enable"); !strings.Contains(out, "stay provisional until attached, and sessions see how many after each message") || strings.Contains(out, "before replying") {
		t.Fatalf("enable output %q", out)
	}
}

func TestLedgerEnableFailsWhenTheDaemonIsDown(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: "down-key"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, args := range [][]string{{"enable"}, {"disable"}} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil || !strings.Contains(err.Error(), "daemon is not running") {
			t.Fatalf("%v with no daemon: %v", args, err)
		}
	}
	if scope, err := ledger.NewStore(store.WriteDB(), store.ReadDB()).Scope("down-key"); err != nil || scope.State != ledger.LedgerUnset {
		t.Fatalf("the ledger changed with no daemon: %+v %v", scope, err)
	}
}

func TestLedgerEnableInAFolderWithoutARepositoryNamesItsProjectsLedger(t *testing.T) {
	app, calls := ledgerWriteFixtureFor(t, registry.ProjectRecord{ID: "p-notes", Title: "notes"}, func(_ string, body map[string]any) (int, any) {
		return http.StatusOK, map[string]any{"projectKey": body["projectKey"], "enabled": body["enabled"], "attach": body["attach"]}
	})
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"enable"}) }); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0]["projectKey"] != registry.LedgerProjectKey("p-notes") {
		t.Fatalf("requests %v, want the ledger of project p-notes", *calls)
	}
}
