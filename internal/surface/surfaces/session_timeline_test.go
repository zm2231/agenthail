package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 0)
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

func TestClaudeTimelineKeepsNonCodexProviderIdentity(t *testing.T) {
	path := timelineFixture(t, `{"type":"assistant","uuid":"claude-message-1","timestamp":"2026-09-07T10:00:00Z","message":{"id":"claude-message-1","content":[{"type":"text","text":"answer"}]}}`+"\n")
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if strings.HasPrefix(page.Items[0].ID, "codex:") || page.Items[0].ID == "" {
		t.Fatalf("Claude item got Codex identity: %+v", page.Items[0])
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
		page, err := readTranscriptPage(context.Background(), path, "codex", cursor, 0)
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
	first, _ := readTranscriptPage(context.Background(), path, "codex", 0, 0)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-449","output":"ok"}}`)
	f.Close()
	second, _ := readTranscriptPage(context.Background(), path, "codex", 0, 0)
	if first.Items[len(first.Items)-1].ID != second.Items[len(second.Items)-2].ID {
		t.Fatal("identity changed on append")
	}
	if second.Items[len(second.Items)-1].Kind != "toolResult" || second.Items[len(second.Items)-1].CallID != "call-449" || second.Items[len(second.Items)-1].Text != "ok" {
		t.Fatalf("function call output=%+v", second.Items[len(second.Items)-1])
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
		page, err := readTranscriptPage(context.Background(), path, "codex", cursor, 0)
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
	page, err := readTranscriptPage(context.Background(), timelineFixture(t, content.String()), "codex", 0, 0)
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
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 0)
	if err != nil || len(page.Items) != 1 || !page.Truncated {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := readTranscriptPage(context.Background(), path, "codex", 999999, 0); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	missing, err := readTranscriptPage(context.Background(), "", "codex", 0, 0)
	if err != nil || missing.UnavailableReason == "" {
		t.Fatal("missing transcript not explicit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readTranscriptPage(ctx, path, "codex", 0, 0); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestCodexTimelineUsesResponseItemsWithoutDuplicateEventMessages(t *testing.T) {
	path := timelineFixture(t, `{"type":"event_msg","payload":{"type":"agent_message","message":"hello"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"commentary","content":[{"type":"output_text","text":"hello"}]}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"never expose this","summary":[{"type":"summary_text","text":"A visible summary"}]}}
{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"patch","input":"*** Begin Patch"}}
`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 0)
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
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Role != "user" || page.Items[0].Text != "Keep the worktree isolated" {
		t.Fatalf("%+v", page)
	}
}

func TestCodexDuplicateUserRepresentationsDoNotDuplicatePrompt(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Continue"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
{"timestamp":"2026-09-07T12:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
`)
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var users []surface.TimelineItem
	for _, item := range page.Items {
		if item.Role == "user" {
			users = append(users, item)
		}
	}
	if len(users) != 2 || users[0].TurnID != "turn-1" || users[1].TurnID != "turn-2" {
		t.Fatalf("users=%+v page=%+v", users, page)
	}
}

func TestCodexSeedAndTailUseTheSameUserProjection(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-10-04T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-10-04T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}
{"timestamp":"2026-10-04T12:00:00Z","type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
`)
	seed, err := readTranscriptPage(context.Background(), path, "codex", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	seedUsers := 0
	for _, item := range seed.Items {
		if item.Role == "user" {
			seedUsers++
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tailUsers := 0
	err = (&Codex{}).Stream(ctx, &surface.Session{ID: "review", Transcript: path, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Role == "user" {
			tailUsers++
			if tailUsers == 2 {
				cancel()
			}
		}
	}, time.Second)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if seedUsers != 1 || tailUsers != 1 {
		t.Fatalf("seed users=%d tail users=%d", seedUsers, tailUsers)
	}
}

func TestCodexEmptySeedPairsUsersByTurnWithoutDroppingLaterUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "review", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy}
	seed, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil || !seed.TranscriptOffsetSet {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
	content := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"type":"event_msg","payload":{"type":"user_message","message":"same"}}
{"type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"same"}]}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"type":"response_item","payload":{"id":"user-2","type":"message","role":"user","content":[{"type":"input_text","text":"same"}]}}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	streamSession := *session
	streamSession.TranscriptOffset = seed.TranscriptOffset
	streamSession.TranscriptOffsetSet = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var events []surface.StreamEvent
	err = codex.Stream(ctx, &streamSession, "", func(event surface.StreamEvent) {
		if event.Role == "user" {
			events = append(events, event)
			if len(events) == 2 {
				cancel()
			}
		}
	}, time.Second)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].TurnID != "turn-1" || events[1].TurnID != "turn-2" {
		t.Fatalf("events=%+v", events)
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

func TestCodexReadUsesLocalTranscriptWithoutNativeRead(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
`)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}
	read, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if read.Source != "local-transcript" || read.UnavailableReason != "" || read.Warning != "" || len(read.Exchanges) != 1 || read.Exchanges[0].Assistant != "hello" || read.Exchanges[0].Source != "local-transcript" {
		t.Fatalf("%+v", read)
	}
	missing := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: filepath.Join(t.TempDir(), "absent.jsonl")}
	read, err = codex.ReadSession(context.Background(), missing, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Exchanges) != 0 || !strings.Contains(read.UnavailableReason, "has not been created yet") || read.Warning != "" {
		t.Fatalf("%+v", read)
	}
	older, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5, Before: 50})
	if err != nil || older.Warning != "" || older.Source != "local-transcript" {
		t.Fatalf("older=%+v err=%v", older, err)
	}
}

func TestCodexDesktopStreamUsesLocalTranscriptHandoffBoundary(t *testing.T) {
	path := timelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
`)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy}
	seed, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil || !seed.TranscriptOffsetSet {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"timestamp":"2026-09-07T12:00:01Z","type":"response_item","payload":{"type":"message","role":"assistant","id":"answer-1","content":[{"type":"output_text","text":"hello"}]}}`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	streamSession := *session
	streamSession.TranscriptOffset = seed.TranscriptOffset
	streamSession.TranscriptOffsetSet = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []surface.StreamEvent
	go func() {
		time.Sleep(100 * time.Millisecond)
		appendFile, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if openErr == nil {
			_, _ = appendFile.WriteString("\n")
			_ = appendFile.Close()
		}
	}()
	err = codex.Stream(ctx, &streamSession, "", func(event surface.StreamEvent) {
		events = append(events, event)
		cancel()
	}, time.Second)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Text != "hello" || events[0].Operation != "upsert" {
		t.Fatalf("events=%+v", events)
	}
}

func TestCodexDesktopStreamCheckpointsOffsetAcrossLifetimes(t *testing.T) {
	path := timelineFixture(t, `{"type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"start"}}
`)
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy}
	seed, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	session.TranscriptOffset = seed.TranscriptOffset
	session.TranscriptOffsetSet = true
	appendRecord := func(text string) {
		t.Helper()
		file, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, writeErr := file.WriteString(`{"type":"response_item","payload":{"type":"message","role":"assistant","turn_id":"turn-1","content":[{"type":"output_text","text":"` + text + `"}]}}
`)
		_ = file.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	appendRecord("first")
	streamOnce := func(want string) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		time.AfterFunc(100*time.Millisecond, cancel)
		var events []surface.StreamEvent
		err := codex.Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Text == want {
				events = append(events, event)
			}
		}, time.Second)
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].Text != want {
			t.Fatalf("want=%q events=%+v", want, events)
		}
	}
	streamOnce("first")
	firstOffset := session.TranscriptOffset
	if firstOffset <= seed.TranscriptOffset {
		t.Fatalf("offset did not advance: seed=%d first=%d", seed.TranscriptOffset, firstOffset)
	}
	appendRecord("second")
	streamOnce("second")
	if session.TranscriptOffset <= firstOffset {
		t.Fatalf("offset did not advance on second lifetime: first=%d second=%d", firstOffset, session.TranscriptOffset)
	}
}

func TestCodexTranscriptPreservesTerminalStatusesAndOpaqueRecords(t *testing.T) {
	for _, status := range []string{"task_cancelled", "task_canceled", "task_aborted", "turn_aborted"} {
		items := codexTimelineItems(map[string]any{"type": "event_msg", "payload": map[string]any{"type": status, "turn_id": "turn-1"}})
		if len(items) != 1 || items[0].Kind != "done" || items[0].Status != status {
			t.Fatalf("status=%q items=%+v", status, items)
		}
	}
	opaque := codexOpaqueTimelineItem(map[string]any{"type": "response_item", "payload": map[string]any{"type": "future_item", "role": "user", "content": strings.Repeat("x", timelineTextBudget*2)}})
	if opaque == nil || opaque.Role != "user" || !opaque.Truncated || len(opaque.Text) > timelineTextBudget+3 || !strings.Contains(opaque.Text, "future_item") {
		t.Fatalf("opaque=%+v", opaque)
	}
	inline := "data:image/png;base64," + strings.Repeat("A", 128)
	redacted := codexOpaqueTimelineItem(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "future_image", "role": "user", "source": map[string]any{"type": "image", "data": inline},
	}})
	if redacted == nil || strings.Contains(redacted.Text, inline) || strings.Contains(redacted.Text, "data:image/") {
		t.Fatalf("opaque image data was retained: %+v", redacted)
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

func TestClaudeReadSessionOlderCursorWalksEveryExchange(t *testing.T) {
	var content strings.Builder
	for i := 1; i <= 150; i++ {
		fmt.Fprintf(&content, `{"type":"user","uuid":"u%03d","timestamp":"2026-09-07T10:00:00Z","message":{"content":"q%03d"}}`+"\n", i, i)
		fmt.Fprintf(&content, `{"type":"assistant","uuid":"a%03d","timestamp":"2026-09-07T10:00:01Z","message":{"id":"m%03d","stop_reason":"end_turn","content":[{"type":"tool_use","id":"call-%03d","name":"Bash","input":{}},{"type":"text","text":"a%03d"}]}}`+"\n", i, i, i, i)
	}
	session := &surface.Session{Transcript: timelineFixture(t, content.String()), Status: surface.StatusIdle}
	var users []string
	seenItems := map[string]bool{}
	cursor := int64(0)
	for page := 0; page < 20; page++ {
		read, err := (&Claude{}).ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 50, Before: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(read.Exchanges) > 50 {
			t.Fatalf("page %d returned %d exchanges", page, len(read.Exchanges))
		}
		var pageUsers []string
		for _, exchange := range read.Exchanges {
			pageUsers = append(pageUsers, exchange.User)
		}
		users = append(pageUsers, users...)
		for _, item := range read.Items {
			if seenItems[item.ID] {
				t.Fatalf("duplicate item %s on page %d", item.ID, page)
			}
			seenItems[item.ID] = true
		}
		if read.NextBefore == 0 {
			break
		}
		if cursor > 0 && read.NextBefore >= cursor {
			t.Fatalf("cursor did not progress: %d -> %d", cursor, read.NextBefore)
		}
		cursor = read.NextBefore
	}
	if len(users) != 150 || len(seenItems) != 450 {
		t.Fatalf("walked %d exchanges and %d items", len(users), len(seenItems))
	}
	for i, user := range users {
		if want := fmt.Sprintf("q%03d", i+1); user != want {
			t.Fatalf("exchange %d: got %q want %q", i, user, want)
		}
	}
}
