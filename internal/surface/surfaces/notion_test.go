package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestNotionCatalogListIsNotComplete(t *testing.T) {
	if NewNotion("", "").CatalogListComplete() {
		t.Fatal("Notion List is bounded and must not reconcile omissions")
	}
}

func TestNotionOnlySupportsPerMessageModelSelection(t *testing.T) {
	notion := &Notion{}
	if notion.Capabilities().Model {
		t.Fatal("Notion advertised a persistent session model")
	}
	if _, err := notion.Model(context.Background(), &surface.Session{}, "model"); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestNotionObservationIsUnknownWithoutAProvenLifecycleState(t *testing.T) {
	observation, err := (&Notion{}).Observe(context.Background(), &surface.Session{ID: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != surface.StatusUnknown {
		t.Fatalf("status=%q", observation.Status)
	}
}

func TestNotionMalformedConfiguredSpaceFailsWithoutPanic(t *testing.T) {
	notion := NewNotion("not-a-uuid", "user")
	_, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "message")
	if err == nil || !strings.Contains(err.Error(), "AGENTHAIL_NOTION_SPACE must be a UUID") {
		t.Fatalf("err=%v", err)
	}
}

func TestNewNotionThreadIDPreservesSpacePortalSegment(t *testing.T) {
	threadID, err := newNotionThreadID("3978aba0-0606-80ac-a1ae-00a9eb229fc0")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(threadID, "-")
	if len(parts) != 5 || parts[1] != "0606" {
		t.Fatalf("threadID=%q", threadID)
	}
}

func TestNotionPostDispatchFailuresHaveUnknownOutcome(t *testing.T) {
	original := notionInferenceRequest
	t.Cleanup(func() { notionInferenceRequest = original })
	for _, test := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{name: "transport", err: context.DeadlineExceeded},
		{name: "http", status: 500, body: "upstream failed"},
		{name: "validation", status: 200, body: `{"type":"error","error":{"message":"failed"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			notionInferenceRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
				return test.status, test.body, test.err
			}
			notion := &Notion{
				spaceID: "00000000-0000-0000-0000-000000000000",
				userID:  "user",
				modelLabels: map[string]string{
					"almond-croissant-low": "Default",
				},
				modelsAt: time.Now(),
			}
			_, err := notion.Send(context.Background(), &surface.Session{ID: "new"}, "message")
			if !surface.IsDeliveryOutcomeUnknown(err) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestValidateNotionInferenceResponse(t *testing.T) {
	success := `{"type":"agent-inference","value":[]}`
	if err := validateNotionInferenceResponse(success); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]string{
		"error":       `{"type":"error","error":{"message":"invalid model"}}`,
		"notionError": `{"isNotionError":true,"message":"denied"}`,
		"empty":       ``,
		"malformed":   `{`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateNotionInferenceResponse(payload); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestNotionErrorDiagnosticIsBounded(t *testing.T) {
	payload := `{"type":"error","error":"` + strings.Repeat("界", 1_000_000) + `"}`
	err := validateNotionInferenceResponse(payload)
	if err == nil || len(err.Error()) > maxDiagnosticBytes+200 {
		t.Fatalf("error bytes=%d", len(err.Error()))
	}
}

func TestNotionRepeatSendsToExistingThreadReturnPerTurnKeys(t *testing.T) {
	originalInference, originalRecord := notionInferenceRequest, notionRecordRequest
	t.Cleanup(func() { notionInferenceRequest, notionRecordRequest = originalInference, originalRecord })
	const threadID = "11111111-1111-1111-1111-111111111111"
	messages := []string{"reply-0"}
	notionInferenceRequest = func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error) {
		next := len(messages)
		messages = append(messages, fmt.Sprintf("user-%d", next), fmt.Sprintf("reply-%d", next+1))
		return 200, `{"type":"agent-inference","value":[]}`, nil
	}
	notionRecordRequest = func(_ context.Context, _ string, _ string, _ map[string]string, body, _, _ string, _ time.Duration) (int, string, error) {
		if strings.Contains(body, `"table":"thread"`) {
			payload, _ := json.Marshal(map[string]any{"recordMap": map[string]any{"thread": map[string]any{threadID: map[string]any{"value": map[string]any{"value": map[string]any{"messages": messages}}}}}})
			return 200, string(payload), nil
		}
		records := map[string]any{}
		for _, id := range messages {
			if strings.HasPrefix(id, "reply-") {
				records[id] = map[string]any{"value": map[string]any{"value": map[string]any{"step": map[string]any{"type": "agent-inference", "value": []map[string]string{{"type": "text", "content": "answer " + id}}}}}}
			}
		}
		payload, _ := json.Marshal(map[string]any{"recordMap": map[string]any{"thread_message": records}})
		return 200, string(payload), nil
	}
	notion := &Notion{
		spaceID:     "00000000-0000-0000-0000-000000000000",
		userID:      "user",
		modelLabels: map[string]string{"almond-croissant-low": "Default"},
		modelsAt:    time.Now(),
	}
	session := &surface.Session{ID: threadID, Surface: surface.KindNotion}
	first, err := notion.Send(context.Background(), session, "one")
	if err != nil || first == nil || !first.Accepted {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := notion.Send(context.Background(), session, "two")
	if err != nil || second == nil || !second.Accepted {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if first.UUID != "reply-2" || second.UUID != "reply-4" {
		t.Fatalf("sends to one thread must return distinct per-turn keys: first=%q second=%q", first.UUID, second.UUID)
	}
}
