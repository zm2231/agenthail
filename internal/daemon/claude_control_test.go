package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type remoteControlPosts struct {
	mu    sync.Mutex
	posts []map[string]any
}

func (p *remoteControlPosts) all() []map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]map[string]any(nil), p.posts...)
}

// busyRemoteControlClaude is a running Claude session attached to Remote
// Control in the middle of a turn. onPost runs for each event posted to it.
func busyRemoteControlClaude(t *testing.T, onPost func(payload map[string]any)) (*runningDaemon, surface.Session, *remoteControlPosts) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	process := claudetest.New(t)
	transcript := process.Transcript("conversation-control", process.Home)
	process.RecordStatus("conversation-control", process.Home, "session_control", "busy", "", time.Time{})
	appendClaudeRecords(t, transcript, `{"type":"user","uuid":"turn-active","timestamp":"2026-10-19T10:00:00Z","message":{"content":"Refactor the parser"}}`)
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := surface.Session{ID: "conversation-control", Bridge: "session_control", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Status: surface.StatusBusy, HasLocal: true, Transcript: transcript}
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	posts := &remoteControlPosts{}
	claude := surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, process.Home, func(_ context.Context, method, url string, _ map[string]string, body string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
		switch {
		case method == "GET" && strings.HasSuffix(url, "/v1/code/sessions/cse_control"):
			return 200, `{"response_shape":{"id":"cse_control","connection_status":"connected"}}`, nil
		case method == "GET" && strings.Contains(url, "/v1/sessions/session_control/events"):
			return 200, `{"data":[],"has_more":false}`, nil
		case method == "POST" && strings.HasSuffix(url, "/v1/code/sessions/cse_control/events"):
			var envelope struct {
				Events []struct {
					Payload map[string]any `json:"payload"`
				} `json:"events"`
			}
			if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope.Events) != 1 {
				t.Errorf("body=%s err=%v", body, err)
				return 400, "", nil
			}
			posts.mu.Lock()
			posts.posts = append(posts.posts, envelope.Events[0].Payload)
			posts.mu.Unlock()
			if onPost != nil {
				onPost(envelope.Events[0].Payload)
			}
			return 200, `{}`, nil
		}
		t.Errorf("unexpected %s %s", method, url)
		return 404, "", nil
	})
	h, _ := runDaemon(t, store, claude)
	return h, session, posts
}

func (h *runningDaemon) historyEvidence(sessionID, result string) string {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/history?sessionId="+sessionID, nil)
	if status != http.StatusOK {
		h.t.Fatalf("history status=%d body=%s", status, payload)
	}
	var decoded struct {
		Items []struct {
			Result   string `json:"result"`
			Evidence string `json:"evidence"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	for _, item := range decoded.Items {
		if item.Result == result {
			return item.Evidence
		}
	}
	return ""
}

func TestSteeringARemoteControlClaudeReportsTransportAcceptanceWithItsRequest(t *testing.T) {
	h, session, posts := busyRemoteControlClaude(t, nil)
	receipt := h.receipt(map[string]any{"action": "steer", "sessionId": session.ID, "message": "Keep the public API unchanged"})
	sent := posts.all()
	if len(sent) != 1 || sent[0]["type"] != "user" {
		t.Fatalf("posted=%v", sent)
	}
	if receipt["evidence"] != string(surface.EvidenceTransportAccepted) || receipt["requestId"] != sent[0]["uuid"] || !strings.Contains(fmt.Sprint(receipt["detail"]), "not yet confirmed") {
		t.Fatalf("a Remote Control steer claimed more than transport acceptance: %v", receipt)
	}
	if evidence := h.historyEvidence(session.ID, fmt.Sprint(sent[0]["uuid"])); evidence != string(surface.EvidenceTransportAccepted) {
		t.Fatalf("history evidence=%q", evidence)
	}
}

func TestClaudeContextCommandReturnsTheOutputJoinedToItsRequest(t *testing.T) {
	var transcript string
	h, session, posts := busyRemoteControlClaude(t, func(payload map[string]any) {
		appendClaudeRecords(t, transcript, fmt.Sprintf(`{"type":"system","subtype":"local_command","uuid":"output","parentUuid":%q,"commandRun":{"command":"context","args":""},"content":"<local-command-stdout>Context Usage 41%%</local-command-stdout>"}`, payload["uuid"]))
	})
	transcript = session.Transcript
	result := h.receipt(map[string]any{"action": "context", "sessionId": session.ID})
	sent := posts.all()
	if len(sent) != 1 || result["evidence"] != string(surface.EvidenceReplyObserved) || result["output"] != "Context Usage 41%" || result["requestId"] != sent[0]["uuid"] {
		t.Fatalf("result=%v posted=%v", result, sent)
	}
	if evidence := h.historyEvidence(session.ID, fmt.Sprint(sent[0]["uuid"])); evidence != string(surface.EvidenceReplyObserved) {
		t.Fatalf("history evidence=%q", evidence)
	}
}
