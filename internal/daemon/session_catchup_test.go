package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func claudeTranscriptLine(t *testing.T, record map[string]any) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

type pageExchange struct {
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	Timestamp time.Time `json:"timestamp"`
	Command   bool      `json:"command"`
}

func readDashboardExchanges(t *testing.T, handler http.Handler, sessionID, query string) []pageExchange {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/session?id="+sessionID+"&timeline=1"+query, nil)
	addBrowserCookie(handler, request)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page struct {
		Exchanges []pageExchange `json:"exchanges"`
		ReadError string         `json:"readError"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.ReadError != "" {
		t.Fatalf("readError=%q", page.ReadError)
	}
	return page.Exchanges
}

// readDashboardExchangesWhen reads the session page until ready holds: a
// journal seeded before serves its page at once and catches up after it.
func readDashboardExchangesWhen(t *testing.T, handler http.Handler, sessionID string, ready func([]pageExchange) bool) []pageExchange {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := readDashboardExchanges(t, handler, sessionID, "")
		if ready(got) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDashboardSessionPageShowsClaudeTurnsWrittenAfterItsSourceStopped(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = 10 * time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	conversation := "conversation-catch-up"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Minute)
	testkit.AppendFile(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "turn-one", "sessionId": conversation, "timestamp": first.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "first question"}}),
		claudeTranscriptLine(t, map[string]any{"type": "assistant", "uuid": "reply-one", "sessionId": conversation, "timestamp": first.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"id": "message-one", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "first answer"}}}}),
	)
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	handler := dashboardRouter(d)

	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 1 || got[0].User != "first question" || got[0].Assistant != "first answer" {
		t.Fatalf("first page exchanges=%+v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		d.sources.mu.Lock()
		running := len(d.sources.sources)
		d.sources.mu.Unlock()
		if running == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("page source did not stop after its handoff lease")
		}
		time.Sleep(5 * time.Millisecond)
	}
	testkit.AppendFile(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "turn-two", "sessionId": conversation, "timestamp": second.Format(time.RFC3339Nano), "origin": map[string]any{"kind": "peer", "name": "fixture peer", "body": "second question", "msg_id": "peer-two"}, "message": map[string]any{"role": "user", "content": "<cross-session-message from-name=\"fixture peer\">second question</cross-session-message>"}}),
		claudeTranscriptLine(t, map[string]any{"type": "assistant", "uuid": "reply-two", "sessionId": conversation, "timestamp": second.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"id": "message-two", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "second answer"}}}}),
	)

	got := readDashboardExchangesWhen(t, handler, conversation, func(got []pageExchange) bool { return len(got) == 2 && got[1].Assistant == "second answer" })
	if len(got) != 2 || got[0].User != "first question" || got[0].Assistant != "first answer" || got[1].User != "second question" || got[1].Assistant != "second answer" {
		t.Fatalf("page after the source stopped=%+v", got)
	}
	if !got[0].Timestamp.Equal(first) || !got[1].Timestamp.Equal(second) {
		t.Fatalf("exchange timestamps=%v,%v want %v,%v", got[0].Timestamp, got[1].Timestamp, first, second)
	}
}

func TestABuiltInCommandRunBetweenTurnsIsTheSessionPagesLatestExchange(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = 10 * time.Millisecond
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	conversation := "conversation-built-in"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	asked := time.Date(2026, 10, 23, 1, 47, 41, 0, time.UTC)
	testkit.AppendFile(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "turn-one", "sessionId": conversation, "origin": map[string]any{"kind": "human"}, "timestamp": asked.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "Reply with the single word READY."}}),
		claudeTranscriptLine(t, map[string]any{"type": "assistant", "uuid": "reply-one", "sessionId": conversation, "timestamp": asked.Add(9 * time.Second).Format(time.RFC3339Nano), "message": map[string]any{"id": "message-one", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "READY"}}}}),
		claudeTranscriptLine(t, map[string]any{"type": "system", "subtype": "turn_duration", "durationMs": 9000, "sessionId": conversation, "timestamp": asked.Add(9 * time.Second).Format(time.RFC3339Nano)}),
	)
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	handler := dashboardRouter(d)
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 1 || got[0].Assistant != "READY" {
		t.Fatalf("first page exchanges=%+v", got)
	}

	ran := asked.Add(8 * time.Minute)
	testkit.AppendFile(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "isMeta": true, "uuid": "caveat", "sessionId": conversation, "timestamp": ran.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a request, and its output goes straight to the user.</local-command-caveat>"}}),
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "command", "parentUuid": "caveat", "sessionId": conversation, "timestamp": ran.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "<command-name>/context</command-name>\n            <command-message>context</command-message>\n            <command-args></command-args>"}}),
		claudeTranscriptLine(t, map[string]any{"type": "system", "subtype": "local_command", "uuid": "output", "parentUuid": "command", "isMeta": false, "sessionId": conversation, "timestamp": ran.Add(time.Second).Format(time.RFC3339Nano), "content": "<local-command-stdout>## Context Usage\n\n**Tokens:** 40.9k / 500k (8%)</local-command-stdout>"}),
	)
	got := readDashboardExchangesWhen(t, handler, conversation, func(got []pageExchange) bool { return len(got) == 2 && got[1].Assistant != "" })
	if len(got) != 2 || got[1].User != "/context" || got[0].Command || !got[1].Command || got[1].Assistant != "## Context Usage\n\n**Tokens:** 40.9k / 500k (8%)" || !got[1].Timestamp.Equal(ran) {
		t.Fatalf("the built-in is not the latest exchange: %+v", got)
	}
}
