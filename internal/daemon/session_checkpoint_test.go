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

const checkpointConversation = "checkpoint-conversation"

type checkpointFixture struct {
	registry *registry.Registry
	session  surface.Session
	adapter  *catchUpClaude
}

func newCheckpointFixture(t *testing.T) *checkpointFixture {
	t.Helper()
	fixture := claudetest.New(t)
	path := filepath.Join(fixture.Root, "projects", "checkpoint-project", checkpointConversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(claudeTurns(checkpointConversation, 1, 3)), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	session := surface.Session{ID: checkpointConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	f := &checkpointFixture{registry: r, session: session, adapter: &catchUpClaude{Claude: surfaces.NewClaude(nil, fixture.Home)}}
	f.start(t)
	if _, found, err := r.SessionTranscriptCheckpoint(checkpointConversation); err != nil || !found {
		t.Fatalf("seed left no checkpoint: found=%v err=%v", found, err)
	}
	return f
}

func (f *checkpointFixture) start(t *testing.T) *sessionSource {
	t.Helper()
	f.adapter.pages, f.adapter.forward = 0, 0
	source := &sessionSource{manager: newSessionSourceManager(f.registry), session: f.session, adapter: f.adapter, epoch: "checkpoint", ctx: context.Background(), appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
	if err := source.loadSeed(); err != nil {
		t.Fatal(err)
	}
	return source
}

func (f *checkpointFixture) journal(t *testing.T) []string {
	t.Helper()
	page, err := f.registry.ReadSessionJournalPage(checkpointConversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var items []string
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Kind != "source-error" {
			items = append(items, fmt.Sprintf("%s %s %s %q", payload.ItemID, payload.Kind, payload.TurnID, payload.Body))
		}
	}
	return items
}

func (f *checkpointFixture) requireCheckpointAtEnd(t *testing.T) {
	t.Helper()
	checkpoint, found, err := f.registry.SessionTranscriptCheckpoint(checkpointConversation)
	info, statErr := os.Stat(f.session.Transcript)
	if err != nil || statErr != nil || !found || checkpoint.Offset != info.Size() {
		t.Fatalf("checkpoint after catch-up %+v found=%v err=%v, want the transcript's end", checkpoint, found, err)
	}
}

func coldCatchUp(t *testing.T, between func(r *registry.Registry, path string)) ([]string, *catchUpClaude) {
	t.Helper()
	f := newCheckpointFixture(t)
	appendToFile(t, f.session.Transcript, claudeTurns(checkpointConversation, 4, 8))
	if between != nil {
		between(f.registry, f.session.Transcript)
	}
	f.start(t)
	f.requireCheckpointAtEnd(t)
	return f.journal(t), f.adapter
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

func TestAnItemWhoseAppendFailedIsCaughtUpByTheNextSourceDespiteLaterCheckpoints(t *testing.T) {
	f := newCheckpointFixture(t)
	appendToFile(t, f.session.Transcript, claudeTurns(checkpointConversation, 4, 8))
	checkpoint, _, err := f.registry.SessionTranscriptCheckpoint(checkpointConversation)
	if err != nil {
		t.Fatal(err)
	}
	read, err := f.adapter.Claude.TranscriptItems(context.Background(), &f.session, surface.TranscriptItemsRequest{From: checkpoint.Offset})
	if err != nil {
		t.Fatal(err)
	}
	var lost string
	for _, item := range read.Items {
		if item.Text == "answer 5" {
			lost = "timeline:" + item.ID
		}
	}
	if lost == "" {
		t.Fatal("no answer 5 in the backlog")
	}
	allow := rejectJournalWrites(t, f.registry, lost)
	failed := f.start(t)
	allow()
	appendToFile(t, f.session.Transcript, claudeTurns(checkpointConversation, 9, 9))
	info, err := os.Stat(f.session.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.recordTranscriptCheckpoint(info.Size(), read.Identity); err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := f.registry.SessionTranscriptCheckpoint(checkpointConversation); err != nil || found {
		t.Fatalf("a source whose append failed recorded checkpoint %+v found=%v err=%v", checkpoint, found, err)
	}
	f.start(t)
	if f.adapter.forward != 0 || f.adapter.pages == 0 {
		t.Fatalf("%d forward reads, %d pages; the next source must page back", f.adapter.forward, f.adapter.pages)
	}
	f.requireCheckpointAtEnd(t)
	journal := strings.Join(f.journal(t), "\n")
	if strings.Count(journal, `"answer 5"`) != 1 || !strings.Contains(journal, `"answer 9"`) {
		t.Fatalf("journal after the failed append:\n%s", journal)
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
	// A stopped source leaves the map before its stream returns and records
	// where it stopped.
	waitForSourcesToStop := func() {
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
		d.sources.running.Wait()
	}
	waitForSourcesToStop()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := r.SessionTranscriptCheckpoint(conversation); err != nil || !found || checkpoint.Offset != info.Size() {
		t.Fatalf("checkpoint after the stream stopped = %+v found=%v err=%v, want offset %d", checkpoint, found, err, info.Size())
	}
	appendToFile(t, path, claudeTurns(conversation, 4, 4))
	readDashboardExchanges(t, handler, conversation, "")
	deadline = time.Now().Add(5 * time.Second)
	waitForSourcesToStop()
	got := readDashboardExchanges(t, handler, conversation, "")
	if len(got) != 4 || got[3].User != "question 4" || got[3].Assistant != "answer 4" {
		t.Fatalf("page after the source stopped=%+v", got)
	}
}

func TestAFailedMergeIntoAJournalRowKeepsTheSourceFromRecordingACheckpoint(t *testing.T) {
	f := newCheckpointFixture(t)
	page, err := f.registry.ReadSessionJournalPage(checkpointConversation, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var row sessionJournalPayload
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Body == "answer 2" {
			row = payload
		}
	}
	if row.ProviderKey == "" {
		t.Fatal("no journal row for answer 2")
	}
	allow := rejectJournalWrites(t, f.registry, row.ProviderKey)
	source := &sessionSource{manager: newSessionSourceManager(f.registry), session: f.session, adapter: f.adapter, epoch: "checkpoint", ctx: context.Background(), appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
	item := surface.TimelineItem{ID: row.ItemID, Kind: row.Kind, Role: row.Role, Text: row.Body, TurnID: "another-turn"}
	if !source.mergeSeedIntoAuthoritativeRow(row.ProviderKey, "upsert", item) {
		t.Fatal("the item did not match its journal row")
	}
	allow()
	window, err := f.registry.SessionJournalAfter(checkpointConversation, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	reported := false
	for _, entry := range window.Entries {
		reported = reported || entry.Kind == "source-error"
	}
	if !reported {
		t.Fatal("a failed merge left no source error on the journal")
	}
	if err := source.recordTranscriptCheckpoint(1, "transcript"); err != nil {
		t.Fatal(err)
	}
	if checkpoint, found, err := f.registry.SessionTranscriptCheckpoint(checkpointConversation); err != nil || found {
		t.Fatalf("a source whose merge failed recorded checkpoint %+v found=%v err=%v", checkpoint, found, err)
	}
}
