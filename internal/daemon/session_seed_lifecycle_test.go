package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type emptySeedSurface struct {
	*daemonSurface
	reads   atomic.Int32
	seedErr error
	started chan struct{}
}

func (s *emptySeedSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads.Add(1)
	if s.seedErr != nil {
		return nil, s.seedErr
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{}}, nil
}

func (s *emptySeedSurface) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestSessionSourcePersistsSuccessfulEmptySeedAcrossColdRestart(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 2)}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor != 0 || adapter.reads.Load() != 1 {
		t.Fatalf("first cursor=%d reads=%d", first.Cursor, adapter.reads.Load())
	}
	first.Cancel()
	deadline := time.After(2 * time.Second)
	for {
		manager.mu.Lock()
		count := len(manager.sources)
		manager.mu.Unlock()
		if count == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("source did not become cold")
		case <-time.After(10 * time.Millisecond):
		}
	}
	second, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	if second.Cursor != 0 || adapter.reads.Load() != 1 {
		t.Fatalf("cold empty seed re-read provider: cursor=%d reads=%d", second.Cursor, adapter.reads.Load())
	}
}

func TestSessionSourceDoesNotPersistFailedSeedAsSuccess(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 2), seedErr: errors.New("provider unavailable")}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	if _, err := manager.prepareStream(context.Background(), &from, adapter); err == nil {
		t.Fatal("failed seed was accepted")
	}
	status, err := reg.SessionJournalSeedStatus(from.ID)
	if err != nil || status != "failed" {
		t.Fatalf("seed status=%q err=%v", status, err)
	}
	if _, err := manager.prepareStream(context.Background(), &from, adapter); err == nil {
		t.Fatal("failed seed did not remain failed")
	}
	if got := adapter.reads.Load(); got != 2 {
		t.Fatalf("failed seed reads=%d, want retry", got)
	}
}

func TestCodexSourceRecoversRecordsAfterSuccessfulSeedAndColdRestart(t *testing.T) {
	_, reg, _, from, _ := daemonFixture(t)
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-seed"}}
{"type":"event_msg","payload":{"type":"user_message","message":"seed request"}}
{"type":"response_item","payload":{"id":"seed-user","type":"message","role":"user","content":[{"type":"input_text","text":"seed request"}]}}
{"type":"response_item","payload":{"id":"seed-answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"seed answer"}]}}
`), 0600); err != nil {
		t.Fatal(err)
	}
	from.Source = "vscode"
	from.Transport = "desktop"
	from.Transcript = transcript
	from.HasLocal = true
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	adapter := surfaces.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	seedPage, err := reg.ReadSessionJournalPage(from.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	first.Cancel()
	waitForSessionSourceGone(t, manager)
	var missed strings.Builder
	fmt.Fprintln(&missed, `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-restart"}}`)
	fmt.Fprintln(&missed, `{"type":"event_msg","payload":{"type":"user_message","message":"after restart"}}`)
	fmt.Fprintln(&missed, `{"type":"response_item","payload":{"id":"restart-user","type":"message","role":"user","content":[{"type":"input_text","text":"after restart"}]}}`)
	fmt.Fprintln(&missed, `{"type":"response_item","payload":{"id":"restart-answer","type":"message","role":"assistant","content":[{"type":"output_text","text":"restart answer"}]}}`)
	for index := 0; index < 100; index++ {
		fmt.Fprintf(&missed, `{"type":"response_item","payload":{"id":"gap-%02d","type":"message","role":"assistant","content":[{"type":"output_text","text":"gap-%02d"}]}}`+"\n", index, index)
	}
	if err := appendFile(t, transcript, missed.String()); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		seen[payload.Body]++
	}
	for _, body := range []string{"after restart", "restart answer"} {
		if seen[body] != 1 {
			t.Fatalf("body %q count=%d page=%+v", body, seen[body], page)
		}
	}
	for _, body := range []string{"seed request", "seed answer"} {
		if seen[body] != 1 {
			t.Fatalf("reseed duplicated body %q count=%d seed=%+v page=%+v", body, seen[body], seedPage, page)
		}
	}
	for index := 0; index < 100; index++ {
		body := fmt.Sprintf("gap-%02d", index)
		if seen[body] != 1 {
			t.Fatalf("offline interval body %q count=%d page=%+v", body, seen[body], page)
		}
	}
}

func appendFile(t *testing.T, path, content string) error {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func waitForSessionSourceGone(t *testing.T, manager *sessionSourceManager) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		manager.mu.Lock()
		count := len(manager.sources)
		manager.mu.Unlock()
		if count == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("source did not become cold")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type identitySeedSurface struct {
	*emptySeedSurface
}

func (s *identitySeedSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.reads.Add(1)
	return &surface.SessionReadResult{Items: []surface.TimelineItem{
		{ID: "item", Kind: "text", CallID: "tool-call", TurnID: "turn"},
		{ID: "call-only", Kind: "toolResult", CallID: "tool-only"},
	}}, nil
}

func TestSessionSourceSeedDoesNotPromoteCallIDToTurnID(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &identitySeedSurface{emptySeedSurface: &emptySeedSurface{daemonSurface: fake, started: make(chan struct{}, 1)}}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ItemID == "item" && (payload.TurnID != "turn" || payload.CallID != "tool-call") {
			t.Fatalf("payload=%+v", payload)
		}
		if payload.ItemID == "call-only" && payload.TurnID != "" {
			t.Fatalf("call id promoted to turn: %+v", payload)
		}
	}
}
