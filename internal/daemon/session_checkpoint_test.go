package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type catchUpClaude struct {
	*surfaces.Claude
	pages, forward int
}

func (c *catchUpClaude) ReadSession(ctx context.Context, session *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	c.pages++
	return c.Claude.ReadSession(ctx, session, request)
}

func (c *catchUpClaude) TranscriptItems(ctx context.Context, session *surface.Session, request surface.TranscriptItemsRequest) (*surface.TranscriptItemsResult, error) {
	read, err := c.Claude.TranscriptItems(ctx, session, request)
	if err == nil && request.Identity != "" {
		c.forward++
	}
	return read, err
}

func claudeTurns(conversation string, first, last int) string {
	var lines []string
	for turn := first; turn <= last; turn++ {
		at := time.Date(2026, 10, 22, 9, 0, turn, 0, time.UTC).Format(time.RFC3339Nano)
		for _, record := range []map[string]any{
			{"type": "user", "uuid": fmt.Sprintf("user-%d", turn), "sessionId": conversation, "timestamp": at, "message": map[string]any{"role": "user", "content": fmt.Sprintf("question %d", turn)}},
			{"type": "assistant", "uuid": fmt.Sprintf("tool-%d", turn), "sessionId": conversation, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "content": []map[string]any{{"type": "tool_use", "id": fmt.Sprintf("call-%d", turn), "name": "Bash", "input": map[string]any{"command": "go test"}}}}},
			{"type": "user", "uuid": fmt.Sprintf("result-%d", turn), "sessionId": conversation, "timestamp": at, "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": fmt.Sprintf("call-%d", turn), "content": "ok"}}}},
			{"type": "assistant", "uuid": fmt.Sprintf("answer-%d", turn), "sessionId": conversation, "timestamp": at, "message": map[string]any{"id": fmt.Sprintf("message-%d", turn), "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": fmt.Sprintf("answer %d", turn)}}}},
			{"type": "system", "subtype": "turn_duration", "sessionId": conversation, "timestamp": at, "durationMs": 1000},
		} {
			line, _ := json.Marshal(record)
			lines = append(lines, string(line))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func appendToFile(t *testing.T, path, data string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func coldCatchUp(t *testing.T, between func(r *registry.Registry, path string)) ([]string, *catchUpClaude) {
	t.Helper()
	fixture := claudetest.New(t)
	const conversation = "checkpoint-conversation"
	path := filepath.Join(fixture.Root, "projects", "checkpoint-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(conversation, 1, 3)), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := &catchUpClaude{Claude: surfaces.NewClaude(nil, fixture.Home)}
	start := func() {
		t.Helper()
		source := &sessionSource{manager: newSessionSourceManager(r), session: session, adapter: adapter, epoch: "checkpoint", ctx: context.Background(), appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
		if err := source.loadSeed(); err != nil {
			t.Fatal(err)
		}
	}
	start()
	if _, found, err := r.SessionTranscriptCheckpoint(conversation); err != nil || !found {
		t.Fatalf("seed left no checkpoint: found=%v err=%v", found, err)
	}
	appendToFile(t, path, claudeTurns(conversation, 4, 8))
	if between != nil {
		between(r, path)
	}
	adapter.pages, adapter.forward = 0, 0
	start()
	page, err := r.ReadSessionJournalPage(conversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		items = append(items, fmt.Sprintf("%s %s %s %q", payload.ItemID, payload.Kind, payload.TurnID, payload.Body))
	}
	checkpoint, found, err := r.SessionTranscriptCheckpoint(conversation)
	info, statErr := os.Stat(path)
	if err != nil || statErr != nil || !found || checkpoint.Offset != info.Size() {
		t.Fatalf("checkpoint after catch-up %+v found=%v err=%v, want the transcript's end", checkpoint, found, err)
	}
	return items, adapter
}

func TestAColdSourceCatchesUpFromItsCheckpointToTheJournalPagingBackWouldBuild(t *testing.T) {
	forward, forwardAdapter := coldCatchUp(t, nil)
	backward, backwardAdapter := coldCatchUp(t, func(r *registry.Registry, _ string) {
		if err := r.ClearSessionTranscriptCheckpoint("checkpoint-conversation"); err != nil {
			t.Fatal(err)
		}
	})
	if forwardAdapter.forward != 1 || forwardAdapter.pages != 0 {
		t.Fatalf("with a checkpoint: %d forward reads, %d pages; want one forward read", forwardAdapter.forward, forwardAdapter.pages)
	}
	if backwardAdapter.forward != 0 || backwardAdapter.pages == 0 {
		t.Fatalf("without a checkpoint: %d forward reads, %d pages; want pages", backwardAdapter.forward, backwardAdapter.pages)
	}
	if strings.Join(forward, "\n") != strings.Join(backward, "\n") {
		t.Fatalf("journal caught up forward:\n%s\n\npaging back:\n%s", strings.Join(forward, "\n"), strings.Join(backward, "\n"))
	}
	if !strings.Contains(strings.Join(forward, "\n"), `"answer 8"`) {
		t.Fatalf("journal misses the last turn:\n%s", strings.Join(forward, "\n"))
	}
}

func TestAColdSourcePagesBackWhenItsTranscriptWasReplaced(t *testing.T) {
	items, adapter := coldCatchUp(t, func(_ *registry.Registry, path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".new", data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".new", path); err != nil {
			t.Fatal(err)
		}
	})
	if adapter.forward != 0 || adapter.pages == 0 {
		t.Fatalf("%d forward reads, %d pages; a replaced transcript must be paged", adapter.forward, adapter.pages)
	}
	if !strings.Contains(strings.Join(items, "\n"), `"answer 8"`) {
		t.Fatalf("journal misses the last turn:\n%s", strings.Join(items, "\n"))
	}
}

func TestAColdSourcePagesBackWhenTheBacklogIsPastTheForwardLimit(t *testing.T) {
	limit := forwardCatchUpLimit
	forwardCatchUpLimit = 64
	t.Cleanup(func() { forwardCatchUpLimit = limit })
	items, adapter := coldCatchUp(t, nil)
	if adapter.forward != 0 || adapter.pages == 0 {
		t.Fatalf("%d forward reads, %d pages; a backlog past the limit must be paged", adapter.forward, adapter.pages)
	}
	if !strings.Contains(strings.Join(items, "\n"), `"answer 8"`) {
		t.Fatalf("journal misses the last turn:\n%s", strings.Join(items, "\n"))
	}
}

func TestACheckpointAfterAFailedAppendIsClearedNotAdvanced(t *testing.T) {
	_, r, _, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(r), session: from, adapter: &catchUpClaude{}, appendBodies: map[string]string{}}
	if err := source.recordTranscriptCheckpoint(100, "transcript"); err != nil {
		t.Fatal(err)
	}
	source.appendFailed = true
	if err := source.recordTranscriptCheckpoint(200, "transcript"); err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := r.SessionTranscriptCheckpoint(from.ID); err != nil || found {
		t.Fatalf("checkpoint after a failed append = %+v found=%v err=%v, want none", checkpoint, found, err)
	}
	if err := source.recordTranscriptCheckpoint(300, "transcript"); err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := r.SessionTranscriptCheckpoint(from.ID); err != nil || !found || checkpoint.Offset != 300 {
		t.Fatalf("next checkpoint = %+v found=%v err=%v", checkpoint, found, err)
	}
}

func TestALiveSourceLeavesItsCheckpointWhereItsStreamStopped(t *testing.T) {
	previousGrace := sessionPageHandoffGrace
	sessionPageHandoffGrace = time.Second
	t.Cleanup(func() { sessionPageHandoffGrace = previousGrace })
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	d, r, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	const conversation = "checkpoint-live"
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(conversation, 1, 2)), 0600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, Cwd: fixture.Home}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d.Surfaces = []surface.Surface{surfaces.NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	if got := readDashboardExchanges(t, handler, conversation, ""); len(got) != 2 {
		t.Fatalf("first page exchanges=%+v", got)
	}
	appendToFile(t, path, claudeTurns(conversation, 3, 3))
	journalHas := func(text string) bool {
		page, err := r.ReadSessionJournalPage(conversation, 0, 200)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			if strings.Contains(string(entry.Payload), text) {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	for !journalHas("answer 3") {
		if time.Now().After(deadline) {
			t.Fatal("the running source did not stream the third turn")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for {
		d.sources.mu.Lock()
		running := len(d.sources.sources)
		d.sources.mu.Unlock()
		if running == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("page source did not stop after its handoff lease")
		}
		time.Sleep(5 * time.Millisecond)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := r.SessionTranscriptCheckpoint(conversation); err != nil || !found || checkpoint.Offset != info.Size() {
		t.Fatalf("checkpoint after the stream stopped = %+v found=%v err=%v, want offset %d", checkpoint, found, err, info.Size())
	}
	appendToFile(t, path, claudeTurns(conversation, 4, 4))
	got := readDashboardExchanges(t, handler, conversation, "")
	if len(got) != 4 || got[3].User != "question 4" || got[3].Assistant != "answer 4" {
		t.Fatalf("page after the source stopped=%+v", got)
	}
}
