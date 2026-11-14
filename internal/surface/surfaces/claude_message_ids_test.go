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
		{"peer message absorbed mid-turn", map[string]any{"type": "attachment", "uuid": "record-5", "attachment": map[string]any{"type": "queued_command", "prompt": "<cross-session-message>Run the tests</cross-session-message>", "source_uuid": "claude-own-1", "origin": map[string]any{"kind": "peer", "msg_id": "peer-2", "name": "voice"}}}, "peer", "peer-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := claudeTimelineItems(tc.record)
			if len(items) != 1 || items[0].Role != tc.role || items[0].MessageID != tc.want {
				t.Fatalf("items=%+v", items)
			}
		})
	}
	reply := claudeTimelineItems(map[string]any{"type": "assistant", "uuid": "record-4", "promptId": "p-1", "message": map[string]any{"id": "m1", "content": []any{map[string]any{"type": "text", "text": "Done"}}}})
	if len(reply) != 1 || reply[0].MessageID != "" || reply[0].PromptID != "" {
		t.Fatalf("a reply carried a prompt id: %+v", reply)
	}
}

func TestClaudeTypedPromptsCarryTheHookPromptID(t *testing.T) {
	items := claudeTimelineItems(map[string]any{"type": "user", "uuid": "record-1", "promptId": "bb6af0fd", "origin": map[string]any{"kind": "human"}, "message": map[string]any{"content": "yes"}})
	if len(items) != 1 || items[0].PromptID != "bb6af0fd" || items[0].MessageID != "record-1" {
		t.Fatalf("items=%+v", items)
	}
}

func TestAPromptClaudeSentItselfKeepsItsPromptIDAndIsNotThePersons(t *testing.T) {
	wakeup := claudeTimelineItems(map[string]any{"type": "user", "uuid": "record-w", "promptId": "wake-1", "isMeta": true, "promptSource": "system", "turnOrigin": "scheduled", "wakeupSource": "loop_wakeup", "message": map[string]any{"role": "user", "content": "Check the verdict; continue the loop."}})
	if len(wakeup) != 1 || wakeup[0].Kind != "event" || wakeup[0].PromptID != "wake-1" {
		t.Fatalf("a scheduled meta prompt: %+v", wakeup)
	}
	plain := claudeTimelineItems(map[string]any{"type": "user", "uuid": "record-s", "promptId": "wake-2", "turnOrigin": "scheduled", "message": map[string]any{"role": "user", "content": "nightly check"}})
	if len(plain) != 1 || plain[0].PromptID != "wake-2" || plain[0].Origin != "scheduled" {
		t.Fatalf("a scheduled prompt must not read as the person's: %+v", plain)
	}
	skill := claudeTimelineItems(map[string]any{"type": "user", "uuid": "record-k", "promptId": "typed-1", "isMeta": true, "message": map[string]any{"role": "user", "content": "Base directory for this skill: ..."}})
	if len(skill) != 1 || skill[0].PromptID != "" {
		t.Fatalf("a meta record inside the person's turn must not carry their prompt id: %+v", skill)
	}
	person := claudeTimelineItems(map[string]any{"type": "user", "uuid": "record-h", "promptId": "sdk-1", "turnOrigin": "human", "promptSource": "sdk", "origin": map[string]any{"kind": "human"}, "message": map[string]any{"role": "user", "content": "reply with ok"}})
	if len(person) != 1 || person[0].Origin != "" || person[0].PromptID != "sdk-1" {
		t.Fatalf("the person's own prompt: %+v", person)
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
