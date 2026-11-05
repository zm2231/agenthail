package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func ledgerWriteFixture(t *testing.T, respond func(path string, body map[string]any) (int, any)) (*App, *[]map[string]any) {
	t.Helper()
	return ledgerWriteFixtureFor(t, registry.ProjectRecord{ID: "proj", Title: "proj", Key: "repo:github.com/acme/widget"}, respond)
}

func ledgerWriteFixtureFor(t *testing.T, project registry.ProjectRecord, respond func(path string, body map[string]any) (int, any)) (*App, *[]map[string]any) {
	t.Helper()
	dir := t.TempDir()
	s1 := surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: dir, Status: surface.StatusIdle}
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"s1": s1}})
	project.WorkspaceRoot = dir
	if err := store.AddProject(project); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(s1); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("AGENTHAIL_SESSION_ID", "s1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	var calls []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body["path"] = r.URL.Path
		if r.URL.Path != "/api/v1/ledger/checkpoint" {
			calls = append(calls, body)
		}
		status, answer := respond(r.URL.Path, body)
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	return app, &calls
}

func TestLedgerAttachPostsTheStructuredAttachAndPrintsTheReadback(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.AttachResult{Readback: "intent ledger: +widget-A1 on widget-G1 (Checkout) with 2 clauses (widget-C1, widget-C2)"}
	})
	out, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"attach", "claude-prompt:1", "--as", "new-ask", "--to", "widget-G1", "--clause", "add a cart", "--clause", "send a receipt"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "intent ledger: +widget-A1 on widget-G1 (Checkout) with 2 clauses (widget-C1, widget-C2)" {
		t.Fatalf("output %q", out)
	}
	got := (*calls)[0]
	want := map[string]any{"path": "/api/v1/ledger/attach", "projectKey": "repo:github.com/acme/widget", "sessionId": "s1", "utteranceId": "claude-prompt:1", "as": "new-ask", "to": "widget-G1", "clauses": []any{"add a cart", "send a receipt"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request %v, want %v", got, want)
	}
}

func TestLedgerProposeNeedsNoUtteranceAndIsMarkedProposed(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.AttachResult{Readback: "ok"}
	})
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"propose", "--to", "widget-A1", "--clause", "cache results"})
	}); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if got["source"] != "proposed" || got["as"] != "extends" || got["utteranceId"] != nil {
		t.Fatalf("request %v", got)
	}
	if _, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"propose", "--to", "widget-A1", "--clause", "x", "--source", "stated"})
	}); err == nil {
		t.Fatal("propose accepted a stated source")
	}
}

func TestLedgerAttachShowsTheDaemonsRefusal(t *testing.T) {
	app, _ := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusConflict, map[string]any{"error": map[string]string{"code": "refused", "message": "stated clauses need the person's captured utterance"}}
	})
	_, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"attach", "claude-prompt:1", "--as", "extends", "--to", "widget-A1", "--clause", "x"})
	})
	if err == nil || err.Error() != "stated clauses need the person's captured utterance" {
		t.Fatalf("err = %v", err)
	}
}

func TestLedgerGoalCreatesThroughTheDaemon(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{"goal": ledger.Goal{ID: "widget-G1", Name: "Checkout flow"}, "created": true}
	})
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"goal", "Checkout", "flow"}) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "widget-G1 created: Checkout flow" || (*calls)[0]["name"] != "Checkout flow" || (*calls)[0]["path"] != "/api/v1/ledger/goal" {
		t.Fatalf("out %q calls %v", out, *calls)
	}
}

func TestLedgerWriteFlagsAreChecked(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.AttachResult{} })
	for _, args := range [][]string{
		{"attach", "claude-prompt:1", "--as", "extends", "--bogus"},
		{"attach", "claude-prompt:1", "--as", "extends", "--to", "a", "--to", "b"},
		{"attach", "--as", "extends"},
		{"attach", "claude-prompt:1"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid commands reached the daemon: %v", *calls)
	}
}
