package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

const leftToAttach = "[intent ledger] The person's words to you in repo:github.com/acme/widget that are not attached yet"

func TestLedgerAttachTakesSeveralFragmentsInOneCall(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) {
		return http.StatusOK, ledger.AttachEachResult{Fragments: []ledger.AttachOutcome{
			{AttachResult: ledger.AttachResult{Readback: "intent ledger: claude-prompt:1 extends widget-A1"}}, {Refused: "widget-A9 is not an ask"}}}
	})
	out, err := captureStdout(t, func() error {
		return app.cmdLedger([]string{"attach", "claude-prompt:1", "--as", "extends", "--to", "widget-A1", "--clause", "--and", "--and", "claude-prompt:2", "--as", "decides", "--to", "widget-A9"})
	})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 fragments were refused") {
		t.Fatalf("a refused fragment should fail the call: %v", err)
	}
	if !strings.Contains(out, "claude-prompt:1 extends widget-A1") || !strings.Contains(out, "claude-prompt:2: refused: widget-A9 is not an ask") {
		t.Fatalf("output %q", out)
	}
	fragments, _ := (*calls)[0]["fragments"].([]any)
	first, _ := fragments[0].(map[string]any)
	second, _ := fragments[1].(map[string]any)
	if len(*calls) != 1 || (*calls)[0]["path"] != "/api/v1/ledger/attach" || len(fragments) != 2 || first["utteranceId"] != "claude-prompt:1" ||
		first["clauses"].([]any)[0] != "--and" || second["utteranceId"] != "claude-prompt:2" || second["as"] != "decides" || second["to"] != "widget-A9" {
		t.Fatalf("one call with both fragments: %v", *calls)
	}
	for _, args := range [][]string{
		{"attach", "claude-prompt:1", "--as", "extends", "--and", "claude-prompt:2", "--as", "decides", "--project", "other"},
		{"attach", "claude-prompt:1", "--as", "extends", "--and", "--as", "decides"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil {
			t.Errorf("ledger %v was accepted", args)
		}
	}
	if len(*calls) != 1 {
		t.Fatalf("a malformed batch reached the daemon: %v", *calls)
	}
}

func TestWorkWritesThatAreCheckpointsShowTheWordsLeftToAttach(t *testing.T) {
	app, _ := ledgerWriteFixture(t, func(path string, _ map[string]any) (int, any) {
		if path == "/api/v1/ledger/checkpoint" {
			return http.StatusOK, map[string]string{"instruction": leftToAttach}
		}
		return http.StatusOK, ledger.WorkResult{Readback: "intent ledger: ok"}
	})
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"add", "rename the flag"}, true},
		{[]string{"start", "widget-W1"}, true},
		{[]string{"review", "widget-W1"}, true},
		{[]string{"comment", "widget-W1", "started"}, false},
		{[]string{"close", "widget-W1", "--reason", "obsolete"}, false},
	} {
		out, err := captureStdout(t, func() error { return app.cmdLedger(c.args) })
		if err != nil || strings.Contains(out, leftToAttach) != c.want {
			t.Errorf("ledger %v printed %q (err %v), want the words left to attach: %v", c.args, out, err, c.want)
		}
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"add", "rename the flag", "--json"}) })
	var result struct {
		Readback         string `json:"readback"`
		LedgerCheckpoint string `json:"ledgerCheckpoint"`
	}
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || result.Readback != "intent ledger: ok" || result.LedgerCheckpoint != leftToAttach {
		t.Fatalf("--json %q err %v", out, err)
	}
}

func TestASessionStartShowsTheWordsLeftToAttach(t *testing.T) {
	withoutSessionContextLimit(t)
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	if err := store.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		asked = append(asked, r.URL.Path+" "+body["sessionId"])
		json.NewEncoder(w).Encode(map[string]string{"instruction": leftToAttach})
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	for _, source := range []string{"startup", "compact"} {
		output := app.sessionContext(strings.NewReader(`{"session_id":"s1","hook_event_name":"SessionStart","source":"`+source+`"}`), surface.KindClaude, 4242, time.Now())
		text, _ := output["hookSpecificOutput"].(map[string]any)["additionalContext"].(string)
		if !strings.Contains(text, leftToAttach) {
			t.Fatalf("%s context %q", source, text)
		}
	}
	if len(asked) != 2 || asked[0] != "/api/v1/ledger/checkpoint s1" {
		t.Fatalf("checkpoint calls %v", asked)
	}
}
