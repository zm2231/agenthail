package claudesurface

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	transcript.PollInterval = 20 * time.Millisecond
	os.Exit(testkit.RunInOwnHome(m.Run))
}

// readTimeline reads a transcript page through the adapter's public ReadSession.
func TestClaudeTimelinePreservesOrderedToolActivity(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"Inspect the build"}}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Check the compiler output"},{"type":"text","text":"I will run the build."},{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","is_error":true,"content":"test failed"}]}}
{"type":"system","subtype":"compact_boundary"}
`)
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 0)
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
	path := surfacetest.TimelineFixture(t, `{"type":"assistant","uuid":"claude-message-1","timestamp":"2026-09-07T10:00:00Z","message":{"id":"claude-message-1","content":[{"type":"text","text":"answer"}]}}`+"\n")
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if strings.HasPrefix(page.Items[0].ID, "codex:") || page.Items[0].ID == "" {
		t.Fatalf("Claude item got Codex identity: %+v", page.Items[0])
	}
}

func TestClaudeReadGroupsTurnsFiltersNonHumanRecordsAndKeepsFullReply(t *testing.T) {
	long := strings.Repeat("界", 3*transcript.TimelineTextBudget)
	assistant := func(id, text string, done bool) string {
		message := map[string]any{"id": id, "content": []map[string]string{{"type": "text", "text": text}}}
		if done {
			message["stop_reason"] = "end_turn"
		}
		record, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": "2026-09-07T10:01:00Z", "message": message})
		return string(record)
	}
	path := surfacetest.TimelineFixture(t, strings.Join([]string{
		`{"type":"user","timestamp":"2026-09-07T09:59:00Z","message":{"content":"<command-name>/compact</command-name>"}}`,
		`{"type":"user","timestamp":"2026-09-07T09:59:30Z","message":{"content":"<local-command-stdout>done</local-command-stdout>"}}`,
		`{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"fix it"}}`,
		assistant("m1", "Looking at the file", false),
		`{"type":"assistant","timestamp":"2026-09-07T10:01:30Z","message":{"id":"m1","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test"}}]}}`,
		`{"type":"user","timestamp":"2026-09-07T10:01:40Z","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]}}`,
		assistant("m1", long, true),
		`{"type":"system","subtype":"stop_hook_summary"}`,
	}, "\n")+"\n")
	read, err := timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Exchanges) != 2 || read.Exchanges[0].User != "/compact" || read.Exchanges[0].Assistant != "done" {
		t.Fatalf("exchanges=%+v", read.Exchanges)
	}
	exchange := read.Exchanges[1]
	if exchange.User != "fix it" || exchange.Origin != "" || exchange.Assistant != long || exchange.Timestamp.IsZero() {
		t.Fatalf("user=%q assistant_bytes=%d timestamp=%v", exchange.User, len(exchange.Assistant), exchange.Timestamp)
	}
	if read.Reply == nil || read.Reply.Text != "Looking at the file\n"+long || read.Reply.UserText != "fix it" || !read.Reply.Done {
		t.Fatalf("reply=%+v", read.Reply)
	}
	if !read.Truncated || len(read.Items) == 0 || len(read.Items[len(read.Items)-1].Text) > transcript.TimelineTextBudget {
		t.Fatalf("items must stay bounded: truncated=%t items=%d", read.Truncated, len(read.Items))
	}
}

func TestClaudeReplyDoneFollowsTranscriptTurnStateNotRegistryStatus(t *testing.T) {
	path := surfacetest.TimelineFixture(t, `{"type":"user","timestamp":"2026-09-07T10:00:00Z","message":{"content":"fix it"}}
{"type":"assistant","timestamp":"2026-09-07T10:01:00Z","message":{"id":"m1","content":[{"type":"text","text":"Looking at the file"}]}}
`)
	staleIdle := &surface.Session{Transcript: path, Status: surface.StatusIdle}
	read, err := timelineClaude().ReadSession(context.Background(), staleIdle, surface.SessionReadRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if read.Reply == nil || read.Reply.Text != "Looking at the file" || read.Reply.Done || read.Reply.Error != "" {
		t.Fatalf("in-progress turn reported as complete: %+v", read.Reply)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	fmt.Fprintln(f, `{"type":"assistant","timestamp":"2026-09-07T10:02:00Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"Done, patched"}]}}`)
	fmt.Fprintln(f, `{"type":"system","subtype":"stop_hook_summary"}`)
	f.Close()
	read, err = timelineClaude().ReadSession(context.Background(), &surface.Session{Transcript: path, Status: surface.StatusBusy}, surface.SessionReadRequest{Limit: 1})
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
	read, err = timelineClaude().ReadSession(context.Background(), staleIdle, surface.SessionReadRequest{Limit: 1})
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
	session := &surface.Session{Transcript: surfacetest.TimelineFixture(t, content.String()), Status: surface.StatusIdle}
	var users []string
	seenItems := map[string]bool{}
	cursor := int64(0)
	for page := 0; page < 20; page++ {
		read, err := timelineClaude().ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 50, Before: cursor})
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
	// Each turn closes when the next starts; the last is still closing.
	if len(users) != 150 || len(seenItems) != 450+149 {
		t.Fatalf("walked %d exchanges and %d items", len(users), len(seenItems))
	}
	for i, user := range users {
		if want := fmt.Sprintf("q%03d", i+1); user != want {
			t.Fatalf("exchange %d: got %q want %q", i, user, want)
		}
	}
}

// timelineClaude reads transcripts named by the session, with no Claude
// config root holding anything.
func timelineClaude() *Claude {
	return NewClaude(nil, "/nonexistent/agenthail-timeline-home")
}
