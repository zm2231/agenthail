package surfaces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudePromptsCarryTheIDTheirSenderGaveThem(t *testing.T) {
	cases := []struct {
		name   string
		record map[string]any
		role   string
		want   string
	}{
		{"typed or posted prompt", map[string]any{"type": "user", "uuid": "record-1", "origin": map[string]any{"kind": "human"}, "message": map[string]any{"content": "Run the tests"}}, "user", "record-1"},
		{"peer message", map[string]any{"type": "user", "uuid": "record-2", "origin": map[string]any{"kind": "peer", "msg_id": "peer-1", "name": "voice"}, "message": map[string]any{"content": "<cross-session-message>Run the tests</cross-session-message>"}}, "peer", "peer-1"},
		{"prompt absorbed mid-turn", map[string]any{"type": "attachment", "uuid": "record-3", "attachment": map[string]any{"type": "queued_command", "prompt": "Run the tests", "source_uuid": "posted-1", "origin": map[string]any{"kind": "human"}}}, "user", "posted-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := claudeTimelineItems(tc.record)
			if len(items) != 1 || items[0].Role != tc.role || items[0].MessageID != tc.want {
				t.Fatalf("items=%+v", items)
			}
		})
	}
	reply := claudeTimelineItems(map[string]any{"type": "assistant", "uuid": "record-4", "message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "Done"}}}})
	if len(reply) != 1 || reply[0].MessageID != "" {
		t.Fatalf("a reply carried a prompt id: %+v", reply)
	}
}

func TestClaudeRemoteControlPostsUnderTheDeliveryID(t *testing.T) {
	fixture := newClaudeRootFixture(t, "b", "b@example.com")
	var posted []string
	claude := NewClaudeWithRequest(twoProfiles, fixture.home, func(ctx context.Context, method, url string, headers map[string]string, body string, cookies CookieSource, timeout time.Duration) (int, string, error) {
		if method == "POST" && strings.HasSuffix(url, "/events") {
			var request struct {
				Events []struct {
					Payload struct {
						UUID string `json:"uuid"`
					} `json:"payload"`
				} `json:"events"`
			}
			if err := json.Unmarshal([]byte(body), &request); err != nil || len(request.Events) != 1 {
				t.Fatalf("body=%s err=%v", body, err)
			}
			posted = append(posted, request.Events[0].Payload.UUID)
		}
		return fixture.request(ctx, method, url, headers, body, cookies, timeout)
	})
	result, err := claude.postMessage(surface.WithMessageUUID(context.Background(), "intent-uuid"), fixture.session, "hello")
	if err != nil || result.UUID != "intent-uuid" || len(posted) != 1 || posted[0] != "intent-uuid" {
		t.Fatalf("result=%+v posted=%v err=%v", result, posted, err)
	}
	if result, err = claude.postMessage(context.Background(), fixture.session, "hello"); err != nil || result.UUID == "" || result.UUID != posted[1] {
		t.Fatalf("a post without a delivery id needs its own: result=%+v posted=%v err=%v", result, posted, err)
	}
}
