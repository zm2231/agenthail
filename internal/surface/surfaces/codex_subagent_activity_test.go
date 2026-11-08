package surfaces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexSubAgentActivityRecord = `{"timestamp":"2026-10-21T21:21:47Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"SubAgentActivity","model":null,"reasoning_effort":null,"id":"call_1f55","kind":"interacted","agent_thread_id":"01a16055","agent_path":"/root"}}}`

func codexSubAgentTimeline(t *testing.T, lines ...string) []surface.TimelineItem {
	t.Helper()
	path := timelineFixture(t, strings.Join(lines, "\n")+"\n")
	page, err := readTimeline(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	paged := codexServedShape(page.Items)
	pagedJSON, _ := json.Marshal(paged)
	streamedJSON, _ := json.Marshal(codexServedShape(codexStreamedItems(t, path)))
	if string(pagedJSON) != string(streamedJSON) {
		t.Fatalf("paged=%s\nstreamed=%s", pagedJSON, streamedJSON)
	}
	return paged
}

func TestASubAgentActivityBesideItsSendMessageReadsAsTheSendAlone(t *testing.T) {
	items := codexSubAgentTimeline(t,
		`{"timestamp":"2026-10-21T21:21:46Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-10-21T21:21:46Z","type":"response_item","payload":{"type":"function_call","status":"completed","name":"send_message","namespace":"collaboration","arguments":"{\"target\":\"/root\",\"message\":\"done\"}","call_id":"call_1f55"}}`,
		codexSubAgentActivityRecord,
		`{"timestamp":"2026-10-21T21:21:48Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1f55","output":""}}`,
	)
	if len(items) != 2 || items[0].Kind != "toolCall" || items[0].CallID != "call_1f55" || items[1].Kind != "toolResult" || items[1].CallID != "call_1f55" {
		t.Fatalf("items=%+v", items)
	}
}

func TestASubAgentActivityWithoutItsCallReadsAsASubAgentNotice(t *testing.T) {
	items := codexSubAgentTimeline(t,
		`{"timestamp":"2026-10-21T21:21:46Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		codexSubAgentActivityRecord,
	)
	if len(items) != 1 || items[0].Kind != "notice" || items[0].Title != "Sub-agent interacted" || items[0].Text != "/root" {
		t.Fatalf("items=%+v", items)
	}
}
