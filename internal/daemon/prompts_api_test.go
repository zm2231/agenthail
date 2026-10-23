package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type promptsBody struct {
	Prompts    []promptItem `json:"prompts"`
	NextBefore string       `json:"nextBefore"`
}

func journalUserMessage(t *testing.T, r *registry.Registry, sessionID, key, body, ts string, full []byte) {
	t.Helper()
	payload := sessionJournalPayload{ItemID: key, ProviderKey: key, Kind: "message", Role: "user", Body: body, TS: ts, TurnID: "turn-" + key}
	entry := registry.SessionJournalEntry{SessionID: sessionID, Kind: "message", ProviderKey: key, ObservedAt: time.Unix(1, 0)}
	if full != nil {
		payload.Truncated, payload.BodyRef = true, "ref-"+key
		entry.BodyRef, entry.FullBody = payload.BodyRef, full
	}
	entry.Payload, _ = json.Marshal(payload)
	if _, _, err := r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: 5000, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
}

func userMessageEntry(sessionID, key, body, ts string) registry.SessionJournalEntry {
	payload, _ := json.Marshal(sessionJournalPayload{ItemID: key, ProviderKey: key, Kind: "message", Role: "user", Body: body, TS: ts})
	return registry.SessionJournalEntry{SessionID: sessionID, Kind: "message", ProviderKey: key, Payload: payload, Bytes: len(payload), ObservedAt: time.Unix(1, 0)}
}

func getPrompts(t *testing.T, handler http.Handler, query string) (int, promptsBody, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/prompts?"+query, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body promptsBody
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, body, response.Body.String()
}

func promptTexts(prompts []promptItem) string {
	texts := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		texts = append(texts, prompt.Text)
	}
	return strings.Join(texts, "|")
}

func TestPromptsAreWhatAPersonTypedNewestFirst(t *testing.T) {
	d, r, _, session, _ := daemonFixture(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("long prompt ", 50)
	for index, body := range []string{
		"first prompt",
		"<environment_context>\n  <cwd>/work</cwd>\n</environment_context>",
		"<codex_internal_context source=\"goal\">\nContinue.\n</codex_internal_context>",
		"<realtime_delegation>\n  <input>spoken</input>\n</realtime_delegation>",
		"<task-notification>\n<task-id>b1</task-id>\n</task-notification>",
		"This session is being continued from a previous conversation that ran out of context.",
		"   ",
		"second prompt",
	} {
		journalUserMessage(t, r, session.ID, fmt.Sprintf("m%d", index), body, fmt.Sprintf("2026-10-20T10:00:0%dZ", index), nil)
	}
	journalUserMessage(t, r, session.ID, "long", long[:40], "2026-10-20T10:00:09Z", []byte(long))
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})

	code, body, raw := getPrompts(t, handler, "sessionId="+url.QueryEscape(session.ID))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if got, want := promptTexts(body.Prompts), long+"|second prompt|first prompt"; got != want {
		t.Fatalf("prompts = %q, want %q", got, want)
	}
	if first := body.Prompts[1]; first.SessionID != session.ID || first.ItemID != "m7" || first.TurnID != "turn-m7" || first.TS != "2026-10-20T10:00:07Z" || body.NextBefore != "" {
		t.Fatalf("prompt = %+v next = %q", first, body.NextBefore)
	}

	code, page, raw := getPrompts(t, handler, "limit=1&q=PROMPT")
	if code != http.StatusOK || len(page.Prompts) != 1 || page.Prompts[0].ItemID != "long" || page.NextBefore == "" {
		t.Fatalf("first page %d %s", code, raw)
	}
	code, page, raw = getPrompts(t, handler, "limit=2&q=PROMPT&before="+url.QueryEscape(page.NextBefore))
	if code != http.StatusOK || promptTexts(page.Prompts) != "second prompt|first prompt" || page.NextBefore != "" {
		t.Fatalf("second page %d %s", code, raw)
	}
}

func TestPromptsBoundTheirQueryAndTheirScan(t *testing.T) {
	d, r, _, session, _ := daemonFixture(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, query := range []string{"limit=0", fmt.Sprintf("limit=%d", promptsMaxLimit+1), "limit=x", "q=" + strings.Repeat("é", promptsMaxQuery+1), "before=nope"} {
		if code, _, raw := getPrompts(t, handler, query); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, code, raw)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/prompts", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", response.Code)
	}

	entries := []registry.SessionJournalEntry{userMessageEntry(session.ID, "typed", "typed long ago", "2026-10-01T00:00:00Z")}
	for index := range promptsScanRows {
		entries = append(entries, userMessageEntry(session.ID, fmt.Sprintf("ctx%d", index), "<environment_context>x</environment_context>", "2026-10-20T00:00:00Z"))
	}
	if _, err := r.ReplaceSessionJournal(session.ID, entries, registry.SessionJournalRetention{Count: len(entries), Bytes: 64 << 20}, 0); err != nil {
		t.Fatal(err)
	}
	code, page, raw := getPrompts(t, handler, "")
	if code != http.StatusOK || len(page.Prompts) != 0 || page.NextBefore == "" {
		t.Fatalf("bounded scan %d %s", code, raw)
	}
	code, page, raw = getPrompts(t, handler, "before="+url.QueryEscape(page.NextBefore))
	if code != http.StatusOK || promptTexts(page.Prompts) != "typed long ago" || page.NextBefore != "" {
		t.Fatalf("continued scan %d %s", code, raw)
	}
}

func TestForkingBeforeATurnForksThroughTheTurnBeforeIt(t *testing.T) {
	d, r, fake, session, _ := daemonFixture(t)
	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	for index, row := range []struct{ turn, role string }{{"turn-1", "user"}, {"turn-1", "assistant"}, {"", "assistant"}, {"turn-2", "user"}, {"turn-2", "assistant"}} {
		payload, _ := json.Marshal(sessionJournalPayload{ItemID: fmt.Sprintf("i%d", index), Kind: "message", Role: row.role, Body: "x", TurnID: row.turn})
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: session.ID, Kind: "message", ProviderKey: fmt.Sprintf("i%d", index), Payload: payload, ObservedAt: time.Unix(1, 0)}, registry.SessionJournalRetention{Count: 100, Bytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	fork := func(options string) *httptest.ResponseRecorder {
		return serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", `{"action":"session-fork","sessionId":"`+session.ID+`","fork":`+options+`}`)
	}
	if w := fork(`{"beforeTurnId":"turn-2","model":"m"}`); w.Code != 200 || adapter.fork != (surface.ForkOptions{Model: "m", LastTurnID: "turn-1"}) {
		t.Fatalf("%d %s %+v", w.Code, w.Body.String(), adapter.fork)
	}
	for options, code := range map[string]string{`{"beforeTurnId":"turn-1"}`: "fork_first_turn", `{"beforeTurnId":"turn-9"}`: "fork_turn_unknown", `{"beforeTurnId":"turn-2","lastTurnId":"turn-1"}`: "fork_point_conflict"} {
		adapter.fork = surface.ForkOptions{}
		if w := fork(options); w.Code < 400 || !strings.Contains(w.Body.String(), code) || adapter.fork != (surface.ForkOptions{}) {
			t.Fatalf("%s: %d %s %+v", options, w.Code, w.Body.String(), adapter.fork)
		}
	}
}
