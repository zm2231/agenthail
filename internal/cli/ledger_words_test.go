package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const wordsKey = "repo:github.com/acme/widget"

func wordsFixture(t *testing.T, handler http.HandlerFunc) *App {
	t.Helper()
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if _, err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: wordsKey}, ""); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if handler != nil {
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		app.catalogDaemonRunning = func() bool { return true }
		app.daemonAPIOverride = func() (*daemonAPIClient, error) {
			return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
		}
	}
	return app
}

func TestLedgerWordsPrintsWhatTheDaemonReadsAroundTheMessage(t *testing.T) {
	text := "> Should search match typos?\nyes, both kinds"
	app := wordsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v1/ledger" || q.Get("view") != "words" || q.Get("id") != "claude-prompt:1" || q.Get("projectKey") != wordsKey {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		if q.Get("context") == "21" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"--context takes 0 to 20 turns"}}`))
			return
		}
		reply := ledger.NewReply("r1", "2026-11-21T10:00:00Z", "Should search match typos?")
		_ = json.NewEncoder(w).Encode(daemonapi.Words{
			ID: "claude-prompt:1", TargetID: "s1", At: "2026-11-21T10:01:00Z", Host: "mini", Text: text,
			Typed: "yes, both kinds", Quoted: []string{"> Should search match typos?"}, Anchor: "entry",
			Context: &ledger.ContextPayload{Status: ledger.ReplyFound, Reply: reply},
			Turns: []daemonapi.WordsTurn{
				{ItemID: "r1", Role: "assistant", TS: "2026-11-21T10:00:00Z", Text: "Should search match typos?"},
				{ItemID: "u1", Role: "user", TS: "2026-11-21T10:01:00Z", Text: text, Message: true},
				{ItemID: "r2", Role: "assistant", TS: "2026-11-21T10:01:01Z", Text: "Added typo matching."},
			},
		})
	})
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"words", "claude-prompt:1", "--context", "1"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  typed: yes, both kinds\n  quoting: > Should search match typos?\n  answering: Should search match typos?\n",
		"turns around it (found by its own entry):\n",
		" agent: Should search match typos?\n",
		" person, this message: > Should search match typos?\n    yes, both kinds\n",
		" agent: Added typo matching.\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("words lack %q:\n%s", want, out)
		}
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"words", "claude-prompt:1", "--context", "21"}) }); err == nil || !strings.Contains(err.Error(), "0 to 20") {
		t.Fatalf("too many turns = %v", err)
	}
}

func TestLedgerWordsSaysPlainlyThatItNeedsTheDaemon(t *testing.T) {
	app := wordsFixture(t, nil)
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"words", "claude-prompt:1"}) })
	if err == nil || err.Error() != errWordsNeedDaemon.Error() || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
