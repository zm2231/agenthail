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
)

func claudeTranscriptLine(t *testing.T, record map[string]any) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func appendClaudeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, line := range lines {
		if _, err := file.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
}

type pageExchange struct {
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	Timestamp time.Time `json:"timestamp"`
}

func readDashboardExchanges(t *testing.T, handler http.Handler, sessionID, query string) []pageExchange {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/session?id="+sessionID+"&timeline=1"+query, nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
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
	appendClaudeTranscript(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "turn-one", "sessionId": conversation, "timestamp": first.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "first question"}}),
		claudeTranscriptLine(t, map[string]any{"type": "assistant", "uuid": "reply-one", "sessionId": conversation, "timestamp": first.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"id": "message-one", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "first answer"}}}}),
	)
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})

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
	appendClaudeTranscript(t, path,
		claudeTranscriptLine(t, map[string]any{"type": "user", "uuid": "turn-two", "sessionId": conversation, "timestamp": second.Format(time.RFC3339Nano), "origin": map[string]any{"kind": "peer", "name": "fixture peer", "body": "second question", "msg_id": "peer-two"}, "message": map[string]any{"role": "user", "content": "<cross-session-message from-name=\"fixture peer\">second question</cross-session-message>"}}),
		claudeTranscriptLine(t, map[string]any{"type": "assistant", "uuid": "reply-two", "sessionId": conversation, "timestamp": second.Add(time.Second).Format(time.RFC3339Nano), "message": map[string]any{"id": "message-two", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "second answer"}}}}),
	)

	got := readDashboardExchangesWhen(t, handler, conversation, func(got []pageExchange) bool { return len(got) == 2 })
	if len(got) != 2 || got[0].User != "first question" || got[0].Assistant != "first answer" || got[1].User != "second question" || got[1].Assistant != "second answer" {
		t.Fatalf("page after the source stopped=%+v", got)
	}
	if !got[0].Timestamp.Equal(first) || !got[1].Timestamp.Equal(second) {
		t.Fatalf("exchange timestamps=%v,%v want %v,%v", got[0].Timestamp, got[1].Timestamp, first, second)
	}
}
