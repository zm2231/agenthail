package surfaces

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

// codexContextTurn is one turn in the shape Codex Desktop writes it: its
// instructions as developer and user-role context messages, the typed
// prompt, hook context, a commentary message and the final answer.
func codexContextTurn(turn, prompt, answer string) string {
	return fmt.Sprintf(`{"timestamp":"2026-10-21T03:32:36Z","type":"event_msg","payload":{"type":"task_started","turn_id":%[1]q}}
{"timestamp":"2026-10-21T03:32:36Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>\nsandboxed\n</permissions instructions>"}]}}
{"timestamp":"2026-10-21T03:32:36Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nBe brief.\n</INSTRUCTIONS>"},{"type":"input_text","text":"<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"}]}}
{"timestamp":"2026-10-21T03:32:36Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%[2]q}]}}
{"timestamp":"2026-10-21T03:32:36Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<canon-recall>\nrecalled\n</canon-recall>"}]}}
{"timestamp":"2026-10-21T03:32:37Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Checking first."}]}}
{"timestamp":"2026-10-21T03:32:38Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":%[3]q}]}}
{"timestamp":"2026-10-21T03:32:38Z","type":"event_msg","payload":{"type":"task_complete","turn_id":%[1]q,"last_agent_message":%[3]q,"error":null}}
`, turn, prompt, answer)
}

func readCodexFixture(t *testing.T, data string, request surface.SessionReadRequest) *surface.SessionReadResult {
	t.Helper()
	path := timelineFixture(t, data)
	read, err := (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, request)
	if err != nil {
		t.Fatal(err)
	}
	return read
}

func TestCodexTurnReadsAsOneExchangeWithItsInstructionsApart(t *testing.T) {
	data := `{"timestamp":"2026-10-21T03:32:36Z","type":"session_meta","payload":{"id":"thread"}}` + "\n" + codexContextTurn("t1", "Say what this is.", "It is a relay.") + codexContextTurn("t2", "And now?", "Still a relay.")
	read := readCodexFixture(t, data, surface.SessionReadRequest{Limit: 10})
	if len(read.Exchanges) != 2 {
		t.Fatalf("exchanges=%+v", read.Exchanges)
	}
	for index, want := range [][2]string{{"Say what this is.", "It is a relay."}, {"And now?", "Still a relay."}} {
		exchange := read.Exchanges[index]
		if exchange.User != want[0] || exchange.Assistant != want[1] || exchange.Turn == nil || exchange.Turn.State != surface.TurnCompleted {
			t.Fatalf("exchange %d=%+v turn=%+v", index, exchange, exchange.Turn)
		}
	}
	instructions := 0
	for _, item := range read.Items {
		switch {
		case item.Kind == "instructions":
			instructions++
			if item.Role != "" || item.Title != "Instructions" || item.TurnID == "" {
				t.Fatalf("instructions item=%+v", item)
			}
		case item.Kind == "message" && (strings.Contains(item.Text, "AGENTS.md") || strings.Contains(item.Text, "canon-recall") || strings.Contains(item.Text, "permissions instructions")):
			t.Fatalf("injected context read as a message: %+v", item)
		}
	}
	if instructions != 6 {
		t.Fatalf("instructions items=%d, want 3 per turn", instructions)
	}
}

func TestCodexUserMessageWithATypedBlockStaysAPrompt(t *testing.T) {
	mixed := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"},
		map[string]any{"type": "input_text", "text": "Fix the build."},
	}}
	if _, ok := codexInstructions(mixed); ok {
		t.Fatal("a message with a typed block read as instructions")
	}
	image := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "<environment_context>\n  <cwd>/work</cwd>\n</environment_context>"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AA=="},
	}}
	if _, ok := codexInstructions(image); ok {
		t.Fatal("a message with an attached image read as instructions")
	}
	quoted := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "What does <environment_context> mean?"},
	}}
	if _, ok := codexInstructions(quoted); ok {
		t.Fatal("a typed question naming a marker read as instructions")
	}
	items := codexTimelineItems(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": mixed["content"]}})
	if len(items) != 1 || items[0].Kind != "message" || items[0].Role != "user" || !strings.Contains(items[0].Text, "Fix the build.") {
		t.Fatalf("mixed message items=%+v", items)
	}
	request := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /project\n\n## My request:\nFix the app."},
	}}
	if _, ok := codexInstructions(request); ok {
		t.Fatal("instructions carrying the request read as instructions")
	}
	enclosed := map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /project\n\n<INSTRUCTIONS>\nBe brief.\n\n## My request:\nFix the app.\n</INSTRUCTIONS>"},
	}}
	items = codexTimelineItems(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": enclosed["content"]}})
	if len(items) != 1 || items[0].Kind != "message" || items[0].Role != "user" {
		t.Fatalf("a request inside the instructions block was hidden: %+v", items)
	}
}

func TestCodexLimitPagesByTurnNotByMessage(t *testing.T) {
	var data strings.Builder
	data.WriteString(`{"timestamp":"2026-10-21T03:32:36Z","type":"session_meta","payload":{"id":"thread"}}` + "\n")
	for turn := 1; turn <= 8; turn++ {
		data.WriteString(codexContextTurn(fmt.Sprintf("t%d", turn), fmt.Sprintf("prompt %d", turn), fmt.Sprintf("answer %d", turn)))
	}
	first := readCodexFixture(t, data.String(), surface.SessionReadRequest{Limit: 3})
	if got := exchangeUsers(first.Exchanges); got != "prompt 6|prompt 7|prompt 8" {
		t.Fatalf("first page=%s", got)
	}
	if first.NextBefore <= 0 {
		t.Fatalf("first page has no cursor: %d", first.NextBefore)
	}
	second := readCodexFixture(t, data.String(), surface.SessionReadRequest{Limit: 3, Before: first.NextBefore})
	if got := exchangeUsers(second.Exchanges); got != "prompt 3|prompt 4|prompt 5" {
		t.Fatalf("second page=%s", got)
	}
	for _, page := range []struct {
		read  *surface.SessionReadResult
		turns []string
	}{{first, []string{"t6", "t7", "t8"}}, {second, []string{"t3", "t4", "t5"}}} {
		instructions := map[string]int{}
		for _, item := range page.read.Items {
			if item.Kind == "instructions" {
				instructions[item.TurnID]++
			}
		}
		for _, turn := range page.turns {
			if instructions[turn] != 3 {
				t.Fatalf("page %v holds %d instructions of %s: %v", page.turns, instructions[turn], turn, instructions)
			}
		}
		if len(instructions) != len(page.turns) {
			t.Fatalf("page %v holds instructions of other turns: %v", page.turns, instructions)
		}
	}
	for _, exchange := range append(first.Exchanges, second.Exchanges...) {
		if !strings.HasPrefix(exchange.Assistant, "answer ") {
			t.Fatalf("exchange=%+v", exchange)
		}
	}
}

func exchangeUsers(exchanges []surface.Exchange) string {
	users := make([]string, 0, len(exchanges))
	for _, exchange := range exchanges {
		users = append(users, exchange.User)
	}
	return strings.Join(users, "|")
}
