package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

func timelineFixture(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeTimelinePreservesOrderedToolActivity(t *testing.T) {
	path := timelineFixture(t, `{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"Inspect the build"}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Check the compiler output"},{"type":"text","text":"I will run the build."},{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","is_error":true,"content":"test failed"}]}}
{"type":"system","subtype":"compact_boundary"}
`)
	page, err := readTranscriptPage(context.Background(), path, "claude", 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"message", "reasoning", "message", "toolCall", "toolResult", "event"}
	if len(page.Items) != len(kinds) {
		t.Fatalf("%+v", page)
	}
	for i, kind := range kinds {
		if page.Items[i].Kind != kind {
			t.Fatalf("item %d: %+v", i, page.Items[i])
		}
	}
	if page.Items[3].CallID != "call-1" || page.Items[4].CallID != "call-1" || page.Items[4].Status != "error" {
		t.Fatal(page.Items)
	}
	if !strings.Contains(page.Items[3].Text, "go test") || page.Items[0].Timestamp == "" {
		t.Fatal(page.Items)
	}
}

func TestCodexTimelinePagingIsOrderedStableAndComplete(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&content, "{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"name\":\"exec_command\",\"call_id\":\"call-%d\",\"arguments\":\"{}\"}}\n", i)
	}
	path := timelineFixture(t, content.String())
	cursor := int64(0)
	seen := map[string]bool{}
	count := 0
	for pageIndex := 0; pageIndex < 5; pageIndex++ {
		page, err := readTranscriptPage(context.Background(), path, "codex", cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate %s", item.ID)
			}
			seen[item.ID] = true
			count++
		}
		if page.NextBefore == 0 {
			break
		}
		if cursor > 0 && page.NextBefore >= cursor {
			t.Fatal("cursor did not progress")
		}
		cursor = page.NextBefore
	}
	if count != 450 {
		t.Fatalf("received %d of 450", count)
	}
	first, _ := readTranscriptPage(context.Background(), path, "codex", 0)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-449","output":"ok"}}`)
	f.Close()
	second, _ := readTranscriptPage(context.Background(), path, "codex", 0)
	if first.Items[len(first.Items)-1].ID != second.Items[len(second.Items)-2].ID {
		t.Fatal("identity changed on append")
	}
}

func TestCodexTimelineSkipsOversizedRecordAndAdvancesCursor(t *testing.T) {
	message := func(text string) string {
		return fmt.Sprintf(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`, text)
	}
	path := timelineFixture(t, message("before")+"\n"+message(strings.Repeat("x", 2*timelineReadBudget))+"\n"+message("after")+"\n")

	var cursor int64
	var texts []string
	truncated := false
	finished := false
	for pageIndex := 0; pageIndex < 8; pageIndex++ {
		page, err := readTranscriptPage(context.Background(), path, "codex", cursor)
		if err != nil {
			t.Fatal(err)
		}
		truncated = truncated || page.Truncated
		for _, item := range page.Items {
			texts = append(texts, item.Text)
		}
		if page.NextBefore == 0 {
			finished = true
			break
		}
		if cursor != 0 && page.NextBefore >= cursor {
			t.Fatalf("pagination stalled at %d", cursor)
		}
		cursor = page.NextBefore
	}
	if !finished || !truncated || !slices.Contains(texts, "before") || !slices.Contains(texts, "after") {
		t.Fatalf("incomplete paging: finished=%t truncated=%t texts=%v", finished, truncated, texts)
	}
}

func TestTimelineBoundsEncodedPayloadAndPreservesUTF8(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 40; i++ {
		record := map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": fmt.Sprint(i), "output": strings.Repeat("\x01界", 12000)}}
		data, _ := json.Marshal(record)
		content.Write(data)
		content.WriteByte('\n')
	}
	page, err := readTranscriptPage(context.Background(), timelineFixture(t, content.String()), "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if len(encoded) > (512<<10)+1024 {
		t.Fatalf("payload %d bytes", len(encoded))
	}
	if !page.Truncated || page.NextBefore == 0 {
		t.Fatal("missing bounds metadata")
	}
	for _, item := range page.Items {
		if !utf8.ValidString(item.Text) {
			t.Fatal("invalid UTF8")
		}
	}
}

func TestTimelinePartialMalformedMissingAndCancelled(t *testing.T) {
	path := timelineFixture(t, "broken\n"+`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`+"\n"+`{"type":"response_item"`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0)
	if err != nil || len(page.Items) != 1 || !page.Truncated {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := readTranscriptPage(context.Background(), path, "codex", 999999); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	missing, err := readTranscriptPage(context.Background(), "", "codex", 0)
	if err != nil || missing.UnavailableReason == "" {
		t.Fatal("missing transcript not explicit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readTranscriptPage(ctx, path, "codex", 0); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestCodexTimelineUsesResponseItemsWithoutDuplicateEventMessages(t *testing.T) {
	path := timelineFixture(t, `{"type":"event_msg","payload":{"type":"agent_message","message":"hello"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"commentary","content":[{"type":"output_text","text":"hello"}]}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"never expose this","summary":[{"type":"summary_text","text":"A visible summary"}]}}
{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"patch","input":"*** Begin Patch"}}
`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Title != "assistant · commentary" || page.Items[1].Text != "A visible summary" || page.Items[2].Text != "*** Begin Patch" {
		t.Fatalf("%+v", page)
	}
}

func TestCodexEventUserContextSurvivesAlongsideTools(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Keep the worktree isolated"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{}"}}
{"timestamp":"2026-09-07T12:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}
`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Role != "user" || page.Items[0].Text != "Keep the worktree isolated" {
		t.Fatalf("%+v", page)
	}
}

func TestCodexDuplicateUserRepresentationsDoNotDuplicatePrompt(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Continue"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
{"timestamp":"2026-09-07T12:01:00Z","type":"event_msg","payload":{"type":"user_message","message":"Continue"}}
`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("%+v", page)
	}
}

func TestClaudeReadGroupsTurnsFiltersNonHumanRecordsAndKeepsFullReply(t *testing.T) {
	long := strings.Repeat("界", 3*timelineTextBudget)
	assistant := func(id, text string, done bool) string {
		message := map[string]any{"id": id, "content": []map[string]string{{"type": "text", "text": text}}}
		if done {
			message["stop_reason"] = "end_turn"
		}
		record, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": "2026-09-07T10:01:00Z", "message": message})
		return string(record)
	}
	path := timelineFixture(t, strings.Join([]string{
		`{"type":"user","timestamp":"2026-09-07T09:59:00Z","message":{"content":"<command-name>/compact</command-name>"}}`,
		`{"type":"user","timestamp":"2026-09-07T09:59:30Z","message":{"content":"<local-command-stdout>done</local-command-stdout>"}}`,
		`{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"fix it"}}`,
		assistant("m1", "Looking at the file", false),
		`{"type":"assistant","timestamp":"2026-09-07T10:01:30Z","message":{"id":"m1","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test"}}]}}`,
		`{"type":"user","timestamp":"2026-09-07T10:01:40Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}`,
		assistant("m1", long, true),
	}, "\n")+"\n")
	read, err := (&Claude{}).ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Exchanges) != 1 {
		t.Fatalf("exchanges=%+v", read.Exchanges)
	}
	exchange := read.Exchanges[0]
	if exchange.User != "fix it" || exchange.Assistant != "Looking at the file\n"+long || exchange.Timestamp.IsZero() {
		t.Fatalf("user=%q assistant_bytes=%d timestamp=%v", exchange.User, len(exchange.Assistant), exchange.Timestamp)
	}
	if read.Reply == nil || read.Reply.Text != exchange.Assistant || read.Reply.UserText != "fix it" || !read.Reply.Done {
		t.Fatalf("reply=%+v", read.Reply)
	}
	if !read.Truncated || len(read.Items) == 0 || len(read.Items[len(read.Items)-1].Text) > timelineTextBudget {
		t.Fatalf("items must stay bounded: truncated=%t items=%d", read.Truncated, len(read.Items))
	}
}

func TestCodexReadFallsBackToTranscriptAndReportsNativeFailure(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
`)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}
	read, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if read.Source != "local-transcript" || read.UnavailableReason != "" || !strings.Contains(read.Warning, "native session read failed") || len(read.Exchanges) != 1 || read.Exchanges[0].Assistant != "hello" || read.Exchanges[0].Source != "local-transcript" {
		t.Fatalf("%+v", read)
	}
	missing := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: filepath.Join(t.TempDir(), "absent.jsonl")}
	read, err = codex.ReadSession(context.Background(), missing, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Exchanges) != 0 || !strings.Contains(read.UnavailableReason, "has not been created yet") || !strings.Contains(read.UnavailableReason, "native session read failed") || read.Warning != "" {
		t.Fatalf("%+v", read)
	}
	older, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5, Before: 50})
	if err != nil || older.Warning != "" || older.Source != "local-transcript" {
		t.Fatalf("older=%+v err=%v", older, err)
	}
}

func TestClaudeReplyDoneFollowsTranscriptTurnStateNotRegistryStatus(t *testing.T) {
	path := timelineFixture(t, `{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"fix it"}}
{"type":"assistant","timestamp":"2026-09-07T10:01:00Z","message":{"id":"m1","content":[{"type":"text","text":"Looking at the file"}]}}
`)
	staleIdle := &surface.Session{Transcript: path, Status: surface.StatusIdle}
	read, err := (&Claude{}).ReadSession(context.Background(), staleIdle, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.Reply == nil || read.Reply.Text != "Looking at the file" || read.Reply.Done || read.Reply.Error != "" {
		t.Fatalf("in-progress turn reported as complete: %+v", read.Reply)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"assistant","timestamp":"2026-09-07T10:02:00Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"Done, patched"}]}}`)
	f.Close()
	read, err = (&Claude{}).ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusBusy}, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.Reply == nil || read.Reply.Text != "Looking at the file\nDone, patched" || !read.Reply.Done || read.Reply.UserText != "fix it" {
		t.Fatalf("completed turn not reported: %+v", read.Reply)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"user","timestamp":"2026-09-07T10:03:00Z","message":{"content":"again"}}`)
	fmt.Fprintln(f, `{"type":"assistant","timestamp":"2026-09-07T10:04:00Z","message":{"id":"m2","content":[{"type":"text","text":"Starting"}]}}`)
	fmt.Fprintln(f, `{"type":"user","timestamp":"2026-09-07T10:05:00Z","message":{"content":"[Request interrupted by user]"}}`)
	f.Close()
	read, err = (&Claude{}).ReadSession(context.Background(), staleIdle, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.Reply == nil || read.Reply.Text != "Starting" || read.Reply.Done || read.Reply.Error == "" {
		t.Fatalf("interrupted turn not reported: %+v", read.Reply)
	}
}
