package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestMobileClaudeCreationReachesNativeCLIAndRegistersReturnedSession(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	executable := filepath.Join(home, "claude")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "$HOME/argv"
if [ "$1" = agents ]; then
 printf '[{"id":"ab12cd34","sessionId":"native-mobile-session","kind":"background","name":"phone-task","cwd":"%s","state":"working"}]\n' "$HOME"
else
 printf 'backgrounded · ab12cd34\n'
fi
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", executable)
	d := New(registry, []surface.Surface{surfaces.NewClaude("", home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-options", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"claude"`) {
		t.Fatal(response.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "claude", "message": "--literal instruction", "cwd": home, "model": "sonnet", "name": "phone-task", "worktree": "phone-work", "agent": "reviewer", "effort": "high", "permissionMode": "plan"})
	request = httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var receipt struct {
		OK      bool                `json:"ok"`
		Session surface.Session     `json:"session"`
		Result  *surface.SendResult `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(response.Body.String(), err)
	}
	if response.Code != 201 || !receipt.OK || receipt.Session.ID != "native-mobile-session" || receipt.Result != nil {
		t.Fatal(response.Body.String())
	}
	if _, err := registry.Session(receipt.Session.ID); err != nil {
		t.Fatal(err)
	}
	readSession := func() map[string]json.RawMessage {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id="+receipt.Session.ID+"&timeline=1", nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var detail map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	pending := readSession()
	if !strings.Contains(string(pending["readSource"]), "journal") || strings.Contains(string(pending["exchanges"]), "First instruction") || !strings.Contains(string(pending["timeline"]), `"items":[]`) {
		t.Fatal(string(pending["readSource"]), string(pending["timeline"]), string(pending["exchanges"]))
	}
	if _, _, err := registry.AppendSessionJournalEntry(registrypkg.SessionJournalEntry{SessionID: receipt.Session.ID, Kind: "text", ProviderKey: "mobile-item", Payload: []byte(`{"itemId":"mobile-item","version":1,"op":"upsert","kind":"text","role":"user","body":"First instruction"}`)}, registrypkg.SessionJournalRetention{Count: 20, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}
	ready := readSession()
	if !strings.Contains(string(ready["exchanges"]), "First instruction") || !strings.Contains(string(ready["readSource"]), "journal") {
		t.Fatal(string(ready["exchanges"]), string(ready["timeline"]))
	}
	args, _ := os.ReadFile(filepath.Join(home, "argv"))
	for _, want := range []string{"--bg\n", "--model\nsonnet", "--name\nphone-task", "--worktree\nphone-work", "--agent\nreviewer", "--effort\nhigh", "--permission-mode\nplan", "--\n--literal instruction", "agents\n--json\n--all"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
}

func TestMobileQueuePreservesIntegratedTurnSettings(t *testing.T) {
	d, registry, _, from, _ := daemonFixture(t)
	_, err := registry.QueueMessageWithOptions(from.ID, "queued", "", surface.SendOptions{TurnOptions: surface.TurnOptions{Effort: "high", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	d.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	for _, want := range []string{`"effort":"high"`, `"mode":"plan"`, `"serviceTier":"fast"`, `"outputSchema":{"type":"object"}`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatal(response.Body.String())
		}
	}
}
