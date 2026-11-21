package codexsurface

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
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func TestCodexDesktopStreamCheckpointsOffsetAcrossLifetimes(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"type":"event_msg","payload":{"type":"user_message","turn_id":"turn-1","message":"start"}}
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
		// The lifetime ends once the stream has read the transcript and
		// checkpointed its offset, however long that read takes.
		transcript.Waiting = cancel
		defer func() { transcript.Waiting = nil }()
		var events []surface.StreamEvent
		err := codex.Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Kind != "commands" {
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

func TestCodexDesktopStreamUsesLocalTranscriptHandoffBoundary(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
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
		if event.Kind == "commands" {
			return
		}
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

func TestCodexReadUsesLocalTranscriptWithoutNativeRead(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hi"}}
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
	if len(read.Exchanges) != 0 || read.UnavailableReason == "" || read.Warning != "" {
		t.Fatalf("%+v", read)
	}
	older, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5, Before: 50})
	if err != nil || older.Warning != "" || older.Source != "local-transcript" {
		t.Fatalf("older=%+v err=%v", older, err)
	}
}

func TestCodexStreamServesACommandSeenWithoutItsCallAsAToolRow(t *testing.T) {
	output := strings.Repeat("x", 34517)
	record := fmt.Sprintf(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/zsh","-lc","make build"],"aggregated_output":%q,"exit_code":0,"status":"completed"}}}`+"\n", output)
	path := surfacetest.TimelineFixture(t, record)
	session := &surface.Session{ID: "observed-item-completed", Transcript: path, TranscriptOffsetSet: true}
	var events []surface.StreamEvent
	err := surfacetest.InDaemon(NewCodex("")).Stream(context.Background(), session, "", func(event surface.StreamEvent) {
		if event.Kind == "commands" {
			return
		}
		events = append(events, event)
	}, 20*time.Millisecond)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream error=%v", err)
	}
	if len(events) != 2 || events[0].Kind != "toolCall" || events[0].Title != "exec_command" || events[0].Text != `{"cmd":"make build"}` || events[1].Kind != "toolResult" || events[1].CallID != "exec-1" || events[1].Text != output {
		t.Fatalf("events=%+v", events)
	}
}

func TestCodexDuplicateUserRepresentationsDoNotDuplicatePrompt(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Continue"}}
{"timestamp":"2026-09-07T12:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
{"timestamp":"2026-09-07T12:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue"}]}}
`)
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
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

func TestCodexEventUserContextSurvivesAlongsideTools(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"Keep the worktree isolated"}}
{"timestamp":"2026-09-07T12:01:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{}"}}
{"timestamp":"2026-09-07T12:02:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done"}]}}
`)
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Role != "user" || page.Items[0].Text != "Keep the worktree isolated" {
		t.Fatalf("%+v", page)
	}
}

func TestCodexSeedAndTailUseTheSameUserProjection(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-10-04T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"timestamp":"2026-10-04T12:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}
{"timestamp":"2026-10-04T12:00:00Z","type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
`)
	seed, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	seedUsers := 0
	for _, item := range seed.Items {
		if item.Role == "user" {
			seedUsers++
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tailUsers := 0
	err = (&Codex{}).Stream(ctx, &surface.Session{ID: "review", Transcript: path, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Role == "user" {
			tailUsers++
			if tailUsers == 2 {
				cancel()
			}
		}
	}, 300*time.Millisecond)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatal(err)
	}
	if seedUsers != 1 || tailUsers != 1 {
		t.Fatalf("seed users=%d tail users=%d", seedUsers, tailUsers)
	}
}

func TestCodexTimelinePagingIsOrderedStableAndComplete(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 450; i++ {
		fmt.Fprintf(&content, "{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"name\":\"exec_command\",\"call_id\":\"call-%d\",\"arguments\":\"{}\"}}\n", i)
	}
	path := surfacetest.TimelineFixture(t, content.String())
	cursor := int64(0)
	seen := map[string]bool{}
	count := 0
	for pageIndex := 0; pageIndex < 5; pageIndex++ {
		page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, cursor, 0)
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
	first, _ := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"response_item","payload":{"type":"function_call_output","call_id":"call-449","output":"ok"}}`)
	f.Close()
	second, _ := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
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
	previous := transcript.TimelineReadBudget
	transcript.TimelineReadBudget = 64 << 10
	t.Cleanup(func() { transcript.TimelineReadBudget = previous })
	path := surfacetest.TimelineFixture(t, message("before")+"\n"+message(strings.Repeat("x", int(2*transcript.TimelineReadBudget)))+"\n"+message("after")+"\n")

	var cursor int64
	var texts []string
	truncated := false
	finished := false
	for pageIndex := 0; pageIndex < 8; pageIndex++ {
		page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, cursor, 0)
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

func TestCodexTimelineUsesResponseItemsWithoutDuplicateEventMessages(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"type":"event_msg","payload":{"type":"agent_message","message":"hello"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","channel":"commentary","content":[{"type":"output_text","text":"hello"}]}}
{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"never expose this","summary":[{"type":"summary_text","text":"A visible summary"}]}}
{"type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","call_id":"patch","input":"*** Begin Patch"}}
`)
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || !strings.Contains(page.Items[0].Title, "commentary") || page.Items[1].Text != "A visible summary" || page.Items[2].Text != "*** Begin Patch" {
		t.Fatalf("%+v", page)
	}
}

func TestCodexTranscriptPreservesTerminalStatusesAndOpaqueRecords(t *testing.T) {
	var records []string
	statuses := []string{"task_cancelled", "task_canceled", "task_aborted", "turn_aborted"}
	for _, status := range statuses {
		records = append(records, `{"type":"event_msg","payload":{"type":"`+status+`","turn_id":"turn-1"}}`)
	}
	inline := "data:image/png;base64," + strings.Repeat("A", 128)
	opaque, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "future_item", "role": "user", "content": strings.Repeat("x", transcript.TimelineTextBudget*2)}})
	image, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "future_image", "role": "user", "source": map[string]any{"type": "image", "data": inline}}})
	records = append(records, string(opaque), string(image))
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, surfacetest.TimelineFixture(t, strings.Join(records, "\n")+"\n"), 0, 0)
	if err != nil || len(page.Items) != len(statuses)+2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for index, status := range statuses {
		if item := page.Items[index]; item.Kind != "done" || item.Status != status {
			t.Fatalf("status=%q item=%+v", status, item)
		}
	}
	future := page.Items[len(statuses)]
	if future.Kind != "event" || future.Title != "Codex · Future item" || future.Text != "" {
		t.Fatalf("opaque=%+v", future)
	}
	redacted := page.Items[len(statuses)+1]
	if strings.Contains(redacted.Text, inline) || strings.Contains(redacted.Text, "data:image/") {
		t.Fatalf("opaque image data was retained: %+v", redacted)
	}
}

func TestCodexTurnStartIsNotAnActivityItem(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"timestamp":"2026-09-07T12:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","model_context_window":258400,"collaboration_mode_kind":"default"}}
{"timestamp":"2026-09-07T12:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Run the checks"}]}}
{"timestamp":"2026-09-07T12:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Checks pass"}]}}
{"timestamp":"2026-09-07T12:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
`)
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Text != "Run the checks" || page.Items[1].Text != "Checks pass" || page.Items[2].Kind != "done" {
		t.Fatalf("%+v", page.Items)
	}
	if page.Items[0].TurnID != "turn-1" || page.Items[1].TurnID != "turn-1" {
		t.Fatalf("turn identity was lost with the start record: %+v", page.Items)
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
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, surfacetest.TimelineFixture(t, content.String()), 0, 0)
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
	path := surfacetest.TimelineFixture(t, "broken\n"+`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`+"\n"+`{"type":"response_item"`)
	page, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 0, 0)
	if err != nil || len(page.Items) != 1 || !page.Truncated {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, path, 999999, 0); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	missing, err := surfacetest.ReadTimeline(context.Background(), &Codex{}, "", 0, 0)
	if err != nil || missing.UnavailableReason == "" {
		t.Fatal("missing transcript not explicit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := surfacetest.ReadTimeline(ctx, &Codex{}, path, 0, 0); err == nil {
		t.Fatal("cancellation ignored")
	}
}
