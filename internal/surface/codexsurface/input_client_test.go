package codexsurface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func clientInputsRollout() string {
	return strings.Join([]string{
		`{"timestamp":"2026-11-26T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-11-26T10:00:01Z","type":"response_item","payload":{"type":"message","id":"msg_env","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","content_item_metadata":[{"provenance":{"type":"harness","hook":false}}]}}}`,
		`{"timestamp":"2026-11-26T10:00:02Z","type":"response_item","payload":{"type":"message","id":"msg_sent","role":"user","content":[{"type":"input_text","text":"Fix the parser"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","content_item_metadata":[{"provenance":{"type":"user"}}]}}}`,
		`{"timestamp":"2026-11-26T10:00:02Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"thread","turn_id":"turn-1","item":{"type":"UserMessage","id":"user-1","client_id":"intent-1","content":[{"type":"text","text":"Fix the parser","text_elements":[]}]}}}`,
		`{"timestamp":"2026-11-26T10:00:05Z","type":"response_item","payload":{"type":"message","id":"msg_reply","role":"assistant","content":[{"type":"output_text","text":"Fixed."}]}}`,
		`{"timestamp":"2026-11-26T10:00:06Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-11-26T10:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"timestamp":"2026-11-26T10:01:01Z","type":"response_item","payload":{"type":"message","id":"msg_typed","role":"user","content":[{"type":"input_text","text":"Now run the tests"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-2","content_item_metadata":[{"provenance":{"type":"user"}}]}}}`,
		`{"timestamp":"2026-11-26T10:01:01Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"thread","turn_id":"turn-2","item":{"type":"UserMessage","id":"user-2","client_id":null,"content":[{"type":"text","text":"Now run the tests","text_elements":[]}]}}}`,
		`{"timestamp":"2026-11-26T10:01:04Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}`,
	}, "\n") + "\n"
}

type clientInput struct{ id, text, origin, messageID, at string }

func clientInputs(items []surface.TimelineItem) []clientInput {
	var inputs []clientInput
	for _, item := range items {
		if item.Kind == "message" && item.Role == "user" && !strings.HasPrefix(item.Text, "<environment_context>") {
			at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
			inputs = append(inputs, clientInput{item.ID, item.Text, item.Origin, item.MessageID, at.UTC().Format(time.RFC3339)})
		}
	}
	return inputs
}

func TestACodexInputCarriesTheClientItCameFromOnceWhetherPagedOrStreamed(t *testing.T) {
	path := surfacetest.TimelineFixture(t, clientInputsRollout())
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var streamed []surface.TimelineItem
	index := map[string]int{}
	session := &surface.Session{ID: "client-inputs", Transcript: path, TranscriptOffsetSet: true}
	err = surfacetest.InDaemon(NewCodex("")).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		item := surface.TimelineItem{ID: event.ID, Kind: event.Kind, Role: event.Role, Origin: event.Origin, Text: event.Text, MessageID: event.MessageID, Timestamp: event.Timestamp.Format(time.RFC3339Nano)}
		if at, ok := index[item.ID]; ok {
			streamed[at] = item
			return
		}
		index[item.ID] = len(streamed)
		streamed = append(streamed, item)
	}, 20*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream error=%v", err)
	}
	paged, live := clientInputs(page.Items), clientInputs(streamed)
	if len(paged) != 2 || len(live) != 2 {
		t.Fatalf("paged=%+v streamed=%+v", paged, live)
	}
	for position, want := range []clientInput{
		{text: "Fix the parser", messageID: "intent-1", at: "2026-11-26T10:00:02Z"},
		{text: "Now run the tests", origin: surface.OriginPerson, at: "2026-11-26T10:01:01Z"},
	} {
		for _, got := range []clientInput{paged[position], live[position]} {
			if got.id == "" || got.text != want.text || got.origin != want.origin || got.messageID != want.messageID || got.at != want.at {
				t.Fatalf("input %d = %+v want %+v", position, got, want)
			}
		}
		if paged[position].id != live[position].id {
			t.Fatalf("input %d is keyed %q paged and %q streamed", position, paged[position].id, live[position].id)
		}
	}
}

func TestACodexInputWithoutAClientCompletionKeepsNoOrigin(t *testing.T) {
	rollout := strings.Join([]string{
		`{"timestamp":"2026-11-26T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-11-26T10:00:02Z","type":"response_item","payload":{"type":"message","id":"msg_a","role":"user","content":[{"type":"input_text","text":"Review the parser"}],"internal_chat_message_metadata_passthrough":{"turn_id":"turn-1","content_item_metadata":[{"provenance":{"type":"user"}}]}}}`,
		`{"timestamp":"2026-11-26T10:00:03Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"thread","turn_id":"turn-1","item":{"type":"UserMessage","id":"user-1","client_id":"intent-1","content":[{"type":"text","text":"Something else","text_elements":[]}]}}}`,
	}, "\n") + "\n"
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, surfacetest.TimelineFixture(t, rollout), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	inputs := clientInputs(page.Items)
	if len(inputs) != 1 || inputs[0].origin != "" || inputs[0].messageID != "" {
		t.Fatalf("a completion for another message attributed this one: %+v", inputs)
	}
}
