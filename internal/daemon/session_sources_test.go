package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	providers "github.com/zm2231/agenthail/internal/surface/surfaces"
)

type sourceCountingSurface struct {
	*daemonSurface
	calls     atomic.Int32
	started   chan struct{}
	events    chan surface.StreamEvent
	items     []surface.TimelineItem
	streamErr error
}

func TestCodexCatchupRejectsReplacementBeforeFirstPageAfterRegistryReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	initial := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-old"}}
{"type":"response_item","payload":{"id":"old","type":"message","role":"assistant","content":[{"type":"output_text","text":"old generation"}]}}
`
	if err := os.WriteFile(transcript, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: transcript, HasLocal: true}
	if err := reg.RegisterSession(from); err != nil {
		reg.Close()
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		reg.Close()
		t.Fatal(err)
	}
	status, _, identity, err := reg.SessionJournalSeedCheckpoint(from.ID)
	if err != nil || status != registry.SessionJournalSeeded || identity == "" {
		first.Cancel()
		reg.Close()
		t.Fatalf("checkpoint status=%q identity=%q err=%v", status, identity, err)
	}
	first.Cancel()
	waitForSessionSourceGone(t, manager)
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}

	rotated := filepath.Join(t.TempDir(), "replacement.jsonl")
	replacement := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-new"}}
{"type":"response_item","payload":{"id":"new","type":"message","role":"assistant","content":[{"type":"output_text","text":"new generation"}]}}
`
	if err := os.WriteFile(rotated, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rotated, transcript); err != nil {
		t.Fatal(err)
	}

	reg, err = registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	manager = newSessionSourceManager(reg)
	second, err := manager.prepareStream(context.Background(), &from, adapter)
	if err == nil {
		second.Cancel()
		t.Fatal("replacement before first catch-up page was accepted")
	}
	if !strings.Contains(err.Error(), "replaced before catch-up") || !errors.Is(err, surface.ErrTranscriptUnavailable) {
		t.Fatalf("err=%v, want typed replacement failure", err)
	}
	status, _, trustedAfterFailure, err := reg.SessionJournalSeedCheckpoint(from.ID)
	if err != nil || status != registry.SessionJournalSeedFailed || trustedAfterFailure != identity {
		t.Fatalf("failed checkpoint status=%q identity=%q err=%v, want preserved %q", status, trustedAfterFailure, err, identity)
	}
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Body == "new generation" {
			t.Fatalf("replacement item was published: %+v", payload)
		}
	}

	third, err := manager.prepareStream(context.Background(), &from, adapter)
	if err == nil {
		third.Cancel()
		t.Fatal("failed retry trusted replacement identity was bypassed")
	}
	status, _, retryIdentity, checkpointErr := reg.SessionJournalSeedCheckpoint(from.ID)
	if checkpointErr != nil || status != registry.SessionJournalSeedFailed || retryIdentity != identity {
		t.Fatalf("retry checkpoint status=%q identity=%q err=%v", status, retryIdentity, checkpointErr)
	}
}

func TestLegacySeedCheckpointNeverAcceptsReplacementAcrossRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"response_item","payload":{"id":"old","type":"message","role":"assistant","content":[{"type":"output_text","text":"old generation"}]}}
`), 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	from := surface.Session{ID: "from", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: transcript, HasLocal: true}
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	oldEntry, changed, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: "codex:old", Payload: []byte(`{"itemId":"old","kind":"text","role":"assistant","body":"old generation"}`)}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes})
	if err != nil || !changed {
		t.Fatalf("old entry=%+v changed=%v err=%v", oldEntry, changed, err)
	}
	if err := reg.MarkSessionJournalSeed(from.ID, true); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(t.TempDir(), "replacement.jsonl")
	if err := os.WriteFile(replacement, []byte(`{"type":"response_item","payload":{"id":"new","type":"message","role":"assistant","content":[{"type":"output_text","text":"new generation"}]}}
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, transcript); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := manager.prepareStream(context.Background(), &from, adapter); err == nil || !strings.Contains(err.Error(), "identity checkpoint is unavailable") {
			t.Fatalf("attempt %d err=%v, want durable missing-identity failure", attempt+1, err)
		}
		waitForSessionSourceGone(t, manager)
	}
	status, seedSeq, identity, err := reg.SessionJournalSeedCheckpoint(from.ID)
	if err != nil || status != registry.SessionJournalSeedFailed || seedSeq != oldEntry.Seq || identity != "" {
		t.Fatalf("checkpoint status=%q seq=%d identity=%q err=%v", status, seedSeq, identity, err)
	}
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	seenOld, seenNew := false, false
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		seenOld = seenOld || payload.Body == "old generation"
		seenNew = seenNew || payload.Body == "new generation"
	}
	if !seenOld || seenNew {
		t.Fatalf("journal mixed generations: old=%v new=%v page=%+v", seenOld, seenNew, page)
	}
}

type restartingSource struct {
	*daemonSurface
	calls atomic.Int32
}

type contextRefreshSurface struct {
	*daemonSurface
	seen chan string
}

func (s *contextRefreshSurface) ContextUsage(_ context.Context, session *surface.Session) (*surface.ContextUsage, error) {
	s.seen <- session.ConfiguredModel
	return &surface.ContextUsage{UsedTokens: 10, ContextWindow: 100}, nil
}

func TestSessionSourceRefreshesConfiguredModelBeforeContextEmission(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	from.ConfiguredModel = "claude-opus-5-5[1m]"
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	adapter := &contextRefreshSurface{daemonSurface: fake, seen: make(chan string, 1)}
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: adapter, epoch: "epoch", appendBodies: map[string]string{}}
	updated := from
	updated.ConfiguredModel = "claude-sonnet-4"
	if err := reg.RegisterSession(updated); err != nil {
		t.Fatal(err)
	}
	source.append(surface.StreamEvent{ID: "context", Kind: "context", Context: &surface.ContextUsage{UsedTokens: 1}})
	select {
	case model := <-adapter.seen:
		if model != updated.ConfiguredModel {
			t.Fatalf("context model=%q, want %q", model, updated.ConfiguredModel)
		}
	case <-time.After(time.Second):
		t.Fatal("context provider was not refreshed")
	}
}

func TestSessionSourceJournalsToolResultAttachmentMetadataWithoutBytes(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	source.append(surface.StreamEvent{ID: "tool-result", ProviderKey: "tool-result", Kind: "toolResult", Role: "user", Title: "Tool result", CallID: "call-1", Text: "caption", Attachment: &surface.Attachment{ID: "attachment:0:0:hash", MediaType: "image/png", Bytes: 4}})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if strings.Contains(string(page.Entries[0].Payload), "base64") || !strings.Contains(string(page.Entries[0].Payload), `"callId":"call-1"`) || !strings.Contains(string(page.Entries[0].Payload), `"attachment"`) {
		t.Fatalf("payload=%s", page.Entries[0].Payload)
	}
}

type seedBarrierSurface struct {
	*sourceCountingSurface
	readStarted chan struct{}
	release     chan struct{}
}

func (s *seedBarrierSurface) StreamCursor(context.Context, *surface.Session) (uint64, error) {
	select {
	case <-s.readStarted:
		return 8, nil
	default:
		return 7, nil
	}
}

func (s *seedBarrierSurface) ReadSession(ctx context.Context, _ *surface.Session, _ surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	close(s.readStarted)
	select {
	case <-s.release:
		return &surface.SessionReadResult{Items: append([]surface.TimelineItem(nil), s.items...)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *seedBarrierSurface) Stream(ctx context.Context, session *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	if !session.StreamCursorSet || session.StreamCursor != 7 {
		return errors.New("stream did not receive the pre-seed cursor barrier")
	}
	onEvent(surface.StreamEvent{ID: "codex:turn-1:assistant:item-1", ProviderKey: "codex:turn-1:assistant:item-1", Cursor: 8, Version: 6, Operation: "append", Kind: "text", Role: "assistant", Text: " world"})
	<-ctx.Done()
	return ctx.Err()
}

func TestSessionSourceTurnPhasePreservesAssistantBody(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	source.append(surface.StreamEvent{ID: "answer", ProviderKey: "answer", Operation: "upsert", Kind: "text", Role: "assistant", Text: "The complete answer", Version: 19})
	source.append(surface.StreamEvent{ID: "answer", ProviderKey: "answer", Operation: "phase", Kind: "done", Version: 19})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 2 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var body, phase sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(page.Entries[1].Payload, &phase); err != nil {
		t.Fatal(err)
	}
	if body.Kind != "text" || body.Body != "The complete answer" || body.ItemID == phase.ItemID || phase.Kind != "done" {
		t.Fatalf("body=%+v phase=%+v", body, phase)
	}
}

func TestSessionJournalInlineBodyKeepsUTF8Boundary(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(reg)
	source := &sessionSource{manager: manager, session: from, adapter: fake, epoch: "test", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	body := strings.Repeat("a", sessionStreamBodyBytes-1) + "é rest"
	source.append(surface.StreamEvent{ID: "unicode", Kind: "text", Text: body})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(payload.Body) || strings.ContainsRune(payload.Body, utf8.RuneError) || !strings.HasPrefix(body, payload.Body) || !payload.Truncated || payload.BodyRef == "" || payload.TruncationReason != "" {
		t.Fatalf("invalid bounded body: %+v", payload)
	}
	full, _, err := reg.SessionJournalBody(from.ID, payload.BodyRef, 0, len(body))
	if err != nil || string(full) != body {
		t.Fatalf("full body mismatch: err=%v", err)
	}
}

func TestSessionJournalBodyBudgetKeepsPreviewWithoutDeadReference(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(reg)
	source := &sessionSource{manager: manager, session: from, adapter: fake, epoch: "test", appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	body := strings.Repeat("x", sessionJournalRetentionBytes)
	source.append(surface.StreamEvent{ID: "oversized", Kind: "text", Text: body})
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Body == "" || !strings.HasPrefix(body, payload.Body) || !payload.Truncated || payload.TruncationReason != "full_body_not_retained" || payload.BodyRef != "" {
		t.Fatalf("payload=%+v", payload)
	}
	if len(payload.Body) > sessionStreamBodyBytes {
		t.Fatalf("inline preview bytes=%d, want <=%d", len(payload.Body), sessionStreamBodyBytes)
	}
}

func (s *restartingSource) Stream(ctx context.Context, _ *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	if s.calls.Add(1) == 1 {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *sourceCountingSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{Items: append([]surface.TimelineItem(nil), s.items...)}, nil
}

func (s *sourceCountingSurface) Stream(ctx context.Context, _ *surface.Session, _ string, onEvent func(surface.StreamEvent), _ time.Duration) error {
	s.calls.Add(1)
	select {
	case s.started <- struct{}{}:
	default:
	}
	if s.streamErr != nil {
		return s.streamErr
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-s.events:
			onEvent(event)
		}
	}
}

func TestSessionSourcePersistsStreamFailureAsReset(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		streamErr:     errors.New(strings.Repeat("upstream unavailable ", 32)),
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	var entry registry.SessionJournalEntry
	select {
	case entry = <-subscription.Entries:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive source failure")
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(entry.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if entry.Seq == 0 || payload.Op != "reset" || payload.Kind != "source-error" || payload.ItemID == "" || payload.Reason == "" || len([]rune(payload.Reason)) > 240 {
		t.Fatalf("entry=%+v payload=%+v", entry, payload)
	}
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 || window.Entries[0].Seq != entry.Seq {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestSessionSourceDoesNotJournalNormalStreamWindow(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		streamErr:     surface.ErrStreamWindow,
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	time.Sleep(50 * time.Millisecond)
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range window.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Kind == "source-error" {
			t.Fatalf("normal stream window journaled source error: %+v", payload)
		}
	}
}

func TestSessionSourceSeedsJournalBeforeSubscribeReturns(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-1", Kind: "text", Text: "seeded"}},
	}
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "seed-1" || payload.Body != "seeded" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestSessionSourceUnifiesCodexSeedAndLiveProviderIdentity(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "codex:turn-1:assistant:item-1", Kind: "text", Text: "seeded"}},
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("seed window=%+v err=%v", window, err)
	}
	var seed sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.ProviderKey != "codex:turn-1:assistant:item-1" {
		t.Fatalf("seed identity=%+v", seed)
	}
	adapter.events <- surface.StreamEvent{ID: seed.ItemID, ProviderKey: seed.ProviderKey, Version: 2, Operation: "upsert", TurnID: "turn-1", Kind: "text", Text: "live"}
	deadline := time.After(time.Second)
	for {
		select {
		case entry := <-subscription.Entries:
			var payload sessionJournalPayload
			if json.Unmarshal(entry.Payload, &payload) == nil && payload.Body == "live" {
				goto liveObserved
			}
		case <-deadline:
			t.Fatal("live event was not journaled")
		}
	}

liveObserved:
	window, err = reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("live window=%+v err=%v", window, err)
	}
	var live sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &live); err != nil {
		t.Fatal(err)
	}
	if live.ProviderKey != seed.ProviderKey || live.Body != "live" {
		t.Fatalf("live identity/body=%+v seed=%+v", live, seed)
	}
}

func TestSessionSourceSeedAndLiveAttachmentShareOneSSEIdentity(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	attachmentHash := strings.Repeat("a", 64)
	attachmentID := "attachment:0:1:" + attachmentHash
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items: []surface.TimelineItem{{
			ID: "codex:turn-image:attachment:user-image-1:0", Kind: "attachment", Role: "user", Text: "Image attachment",
			Attachment: &surface.Attachment{ID: attachmentID, MediaType: "image/png", Width: 1, Height: 1, Bytes: 68},
		}},
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("seed window=%+v err=%v", window, err)
	}
	var seed sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &seed); err != nil {
		t.Fatal(err)
	}
	if seed.ProviderKey != "codex:turn-image:attachment:user-image-1:0" || seed.Attachment == nil || seed.Attachment.ID != attachmentID {
		t.Fatalf("seed=%+v", seed)
	}
	adapter.events <- surface.StreamEvent{ID: seed.ItemID, ProviderKey: seed.ProviderKey, Version: 2, Operation: "upsert", TurnID: "turn-image", Kind: "attachment", Role: "user", Text: "Image attachment", Attachment: seed.Attachment}
	deadline := time.After(time.Second)
	for {
		select {
		case entry := <-subscription.Entries:
			var payload sessionJournalPayload
			if json.Unmarshal(entry.Payload, &payload) == nil && payload.Version == 2 {
				window, err = reg.SessionJournalAfter(from.ID, 0, 10)
				if err != nil || len(window.Entries) != 1 || payload.Attachment == nil || payload.Attachment.ID != seed.Attachment.ID {
					t.Fatalf("live window=%+v payload=%+v err=%v", window, payload, err)
				}
				return
			}
		case <-deadline:
			t.Fatal("live attachment was not published")
		}
	}
}

func TestSessionSourceSeedsWithoutUnsupportedLiveStream(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	from.Transport = "uds"
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-uds", Kind: "text", Text: "peer seed"}},
	}
	adapter.caps.Stream = false
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "seed-uds" || payload.Kind == "source-error" || adapter.calls.Load() != 0 {
		t.Fatalf("payload=%+v streamCalls=%d", payload, adapter.calls.Load())
	}
}

func TestSessionSourceKeepsUnsupportedTailOpenWithoutRetryNoise(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	from.Surface = surface.KindClaude
	from.Transport = "uds"
	from.Transcript = "/local/transcript.jsonl"
	from.HasLocal = true
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items:         []surface.TimelineItem{{ID: "seed-unsupported", Kind: "text", Text: "seed"}},
		streamErr:     surface.ErrUnsupported,
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	time.Sleep(50 * time.Millisecond)
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("unsupported provider calls=%d", calls)
	}
	if window, err := reg.SessionJournalAfter(from.ID, 0, 10); err != nil || len(window.Entries) != 1 {
		t.Fatalf("retry/source-error window=%+v err=%v", window, err)
	}
	subscription.Cancel()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-subscription.Entries:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("source did not close after cancellation")
		}
	}
}

func TestClaudeSessionSourceSeedsAndTailsLocalTranscript(t *testing.T) {
	_, reg, _, from, _ := daemonFixture(t)
	transcript := t.TempDir() + "/session.jsonl"
	seed := `{"type":"user","uuid":"u1","message":{"content":"seed"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"seed answer"}]}}
`
	if err := os.WriteFile(transcript, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}
	from.Surface = surface.KindClaude
	from.Transport = "uds"
	from.Transcript = transcript
	from.HasLocal = true
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewClaude("Default", t.TempDir())
	manager := newSessionSourceManager(reg)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	window, err := reg.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) < 2 {
		t.Fatalf("seed window=%+v err=%v", window, err)
	}
	seedBodies := map[string]string{"seed": "user", "seed answer": "assistant"}
	seenSeed := map[string]bool{}
	var seedThrough uint64
	for _, entry := range window.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if role, ok := seedBodies[payload.Body]; ok && payload.Role == role {
			if seenSeed[payload.Body] {
				t.Fatalf("duplicate seed record body=%q window=%+v", payload.Body, window)
			}
			seenSeed[payload.Body] = true
			if entry.Seq > seedThrough {
				seedThrough = entry.Seq
			}
		}
	}
	if len(seenSeed) != len(seedBodies) {
		t.Fatalf("seed prefix missing: seen=%v window=%+v", seenSeed, window)
	}
	if seedThrough == 0 {
		t.Fatalf("seed checkpoint missing: %+v", window.Entries[:2])
	}
	time.Sleep(100 * time.Millisecond)
	appendTranscript := func(records string) {
		t.Helper()
		file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(records); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, turn := range []struct{ message, turnID, answer, records string }{
		{"m2", "u2", "second answer", `{"type":"user","uuid":"u2","message":{"content":"second"}}
{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"second answer"}]}}
`},
		{"m3", "u3", "third answer", `{"type":"user","uuid":"u3","message":{"content":"third"}}
{"type":"assistant","uuid":"a3","message":{"id":"m3","stop_reason":"end_turn","content":[{"type":"text","text":"third answer"}]}}
`},
	} {
		appendTranscript(turn.records)
		deadline := time.After(3 * time.Second)
		answerID := ""
		for !seen[turn.message] {
			select {
			case entry := <-subscription.Entries:
				if entry.Seq <= seedThrough {
					continue
				}
				var payload sessionJournalPayload
				if err := json.Unmarshal(entry.Payload, &payload); err == nil && payload.TurnID == turn.turnID {
					if payload.Kind == "message" && payload.Role == "assistant" && payload.Body == turn.answer {
						answerID = payload.ItemID
					}
					if payload.Kind == "done" {
						if answerID == "" || answerID == payload.ItemID {
							t.Fatalf("completion lost or overwrote answer: answer=%q done=%+v", answerID, payload)
						}
						seen[turn.message] = true
					}
				}
			case <-deadline:
				t.Fatalf("provider did not tail turn %s", turn.message)
			}
		}
	}
}

func TestClaudeSessionSourceJournalsFullTimelineAcrossTransports(t *testing.T) {
	for _, transport := range []string{"", "uds"} {
		t.Run(map[string]string{"": "native", "uds": "uds"}[transport], func(t *testing.T) {
			_, reg, _, from, _ := daemonFixture(t)
			from.ID = "claude-" + map[string]string{"": "native", "uds": "uds"}[transport]
			from.Surface = surface.KindClaude
			from.Transport = transport
			from.HasLocal = true
			transcript := filepath.Join(t.TempDir(), "session.jsonl")
			seed := `{"type":"user","uuid":"u1","message":{"content":"seed"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"seed answer"}]}}
`
			if err := os.WriteFile(transcript, []byte(seed), 0600); err != nil {
				t.Fatal(err)
			}
			from.Transcript = transcript
			if err := reg.RegisterSession(from); err != nil {
				t.Fatal(err)
			}
			adapter := providers.NewClaude("Default", t.TempDir())
			manager := newSessionSourceManager(reg)
			subscription, err := manager.subscribe(&from, adapter)
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Cancel()

			file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString(`{"type":"user","uuid":"u2","message":{"content":"inspect"}}
{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":null,"content":[{"type":"tool_use","id":"call-2","name":"Read","input":{"path":"x"}},{"type":"thinking","thinking":"checking"}]}}
{"type":"user","uuid":"u2-result","message":{"content":[{"type":"tool_result","tool_use_id":"call-2","content":"contents"}]}}
{"type":"assistant","uuid":"a3","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"final answer"}]}}
`)
			if closeErr := file.Close(); err != nil {
				t.Fatal(err)
			} else if closeErr != nil {
				t.Fatal(closeErr)
			}

			deadline := time.Now().Add(3 * time.Second)
			for {
				window, readErr := reg.SessionJournalAfter(from.ID, 0, 100)
				if readErr != nil {
					t.Fatal(readErr)
				}
				seen := map[string]bool{}
				for _, entry := range window.Entries {
					var payload sessionJournalPayload
					if json.Unmarshal(entry.Payload, &payload) != nil || payload.TurnID != "u2" {
						continue
					}
					switch {
					case payload.Role == "user" && payload.Kind == "message" && payload.Body == "inspect":
						seen["user"] = true
					case payload.Kind == "toolCall" && payload.CallID == "call-2":
						seen["call"] = true
					case payload.Kind == "reasoning" && payload.Body == "checking":
						seen["reasoning"] = true
					case payload.Kind == "toolResult" && payload.CallID == "call-2":
						seen["result"] = true
					case payload.Role == "assistant" && payload.Kind == "message" && payload.Body == "final answer":
						seen["final"] = true
					case payload.Kind == "done":
						seen["done"] = true
					}
				}
				if len(seen) == 6 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("transport=%q timeline=%v window=%+v", transport, seen, window)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestClaudeSessionSourceJournalsInterruptedTerminalBeforeNextTurn(t *testing.T) {
	for _, transport := range []string{"", "uds"} {
		t.Run(map[string]string{"": "native", "uds": "uds"}[transport], func(t *testing.T) {
			_, reg, _, from, _ := daemonFixture(t)
			from.ID = "claude-interrupted-" + map[string]string{"": "native", "uds": "uds"}[transport]
			from.Surface = surface.KindClaude
			from.Transport = transport
			from.HasLocal = true
			transcript := filepath.Join(t.TempDir(), "session.jsonl")
			seed := `{"type":"user","uuid":"u0","message":{"content":"seed"}}
{"type":"assistant","uuid":"a0","message":{"id":"m0","stop_reason":"end_turn","content":[{"type":"text","text":"seed answer"}]}}
`
			if err := os.WriteFile(transcript, []byte(seed), 0600); err != nil {
				t.Fatal(err)
			}
			from.Transcript = transcript
			if err := reg.RegisterSession(from); err != nil {
				t.Fatal(err)
			}
			adapter := providers.NewClaude("Default", t.TempDir())
			subscription, err := newSessionSourceManager(reg).subscribe(&from, adapter)
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Cancel()

			file, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = file.WriteString(`{"type":"user","uuid":"u1","message":{"content":"cancel me"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":null,"content":[{"type":"text","text":"partial"}]}}
{"type":"user","uuid":"interrupt","message":{"content":"[Request interrupted by user]"}}
{"type":"system","subtype":"turn_duration","content":"done"}
{"type":"user","uuid":"u2","message":{"content":"continue"}}
{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"continued"}]}}
`)
			if closeErr := file.Close(); err != nil {
				t.Fatal(err)
			} else if closeErr != nil {
				t.Fatal(closeErr)
			}

			deadline := time.Now().Add(3 * time.Second)
			for {
				window, readErr := reg.SessionJournalAfter(from.ID, 0, 100)
				if readErr != nil {
					t.Fatal(readErr)
				}
				cancelled := 0
				continued := false
				continuedDone := false
				for _, entry := range window.Entries {
					var payload sessionJournalPayload
					if json.Unmarshal(entry.Payload, &payload) != nil {
						continue
					}
					if payload.TurnID == "u1" && payload.Kind == "done" {
						if payload.Status != "cancelled" {
							t.Fatalf("interrupted terminal overwritten: %+v", payload)
						}
						cancelled++
					}
					if payload.TurnID == "u2" && payload.Kind == "message" && payload.Body == "continued" {
						continued = true
					}
					if payload.TurnID == "u2" && payload.Kind == "done" {
						continuedDone = true
					}
				}
				if cancelled == 1 && continued && continuedDone {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("transport=%q cancelled=%d continued=%v done=%v window=%+v", transport, cancelled, continued, continuedDone, window)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestSessionSourceSharesOneUpstreamAndJournalsNormalizedEvents(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 1)}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	first, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	third, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()
	defer second.Cancel()
	defer third.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	if calls := adapter.calls.Load(); calls != 1 {
		t.Fatalf("upstream stream calls=%d", calls)
	}
	adapter.events <- surface.StreamEvent{ID: "m1", ProviderKey: "m1", Version: 4, Operation: "append", TurnID: "t1", Kind: "text", Text: "test"}
	for _, subscription := range []sessionSourceSubscription{first, second, third} {
		select {
		case entry := <-subscription.Entries:
			var payload sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if entry.Seq != 1 || payload.ItemID != "m1" || payload.ProviderKey != "m1" || payload.Op != "upsert" || payload.Body != "test" || payload.TurnID != "t1" {
				t.Fatalf("entry=%+v payload=%+v", entry, payload)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestPrepareStreamDropsSeedBufferAndKeepsFastPostCursorEvent(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent, 1),
		items:         []surface.TimelineItem{{ID: "seed", Kind: "text", Role: "assistant", Text: "seed"}},
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscription, err := manager.prepareStream(ctx, &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	if subscription.Cursor != 1 {
		t.Fatalf("cursor=%d, want seed watermark 1", subscription.Cursor)
	}
	adapter.events <- surface.StreamEvent{ID: "reply", ProviderKey: "reply", Version: 1, Operation: "upsert", Kind: "text", Role: "assistant", TurnID: "turn-fast", Text: "fast reply"}
	select {
	case event := <-subscription.Events:
		if event.Seq <= subscription.Cursor || event.Body != "fast reply" || event.TurnID != "turn-fast" {
			t.Fatalf("event=%+v cursor=%d", event, subscription.Cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("post-cursor event was dropped")
	}
}

func TestSessionSourceAccumulatesStableProviderAppendIntoOneJournalEntry(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent, 2)}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	adapter.events <- surface.StreamEvent{ID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: 3, Operation: "append", TurnID: "turn-1", Kind: "text", Text: "hel"}
	adapter.events <- surface.StreamEvent{ID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: 5, Operation: "append", TurnID: "turn-1", Kind: "text", Text: "lo"}
	for range 2 {
		select {
		case <-subscription.Entries:
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil || payload.ItemID != "managed:turn-1:text" || payload.Body != "hello" || payload.Op != "upsert" {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
}

func TestSessionSourceAssignsIdentityToProviderEventsWithoutOne(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(registry)
	source := &sessionSource{manager: manager, session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	first := source.normalizeLocked(surface.StreamEvent{Kind: "context"})
	second := source.normalizeLocked(surface.StreamEvent{Kind: "context"})
	if first.ItemID == "" || first.ProviderKey == "" || second.ItemID == "" || first.ItemID == second.ItemID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestSessionSourceNormalizesGoalUpdatesAndClears(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	manager := newSessionSourceManager(registry)
	source := &sessionSource{manager: manager, session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	budget := int64(500)
	updated := source.normalizeLocked(surface.StreamEvent{ID: "goal:1", ProviderKey: "goal:1", Kind: "goal", Operation: "replace", Goal: &surface.GoalState{Objective: "verify", Status: surface.GoalStatusPaused, TimeUsedSeconds: 12, TokensUsed: 34, TokenBudget: &budget}})
	encoded, err := json.Marshal(updated)
	if err != nil || updated.Goal == nil || updated.Goal.Status != surface.GoalStatusPaused || *updated.Goal.TokenBudget != 500 || !strings.Contains(string(encoded), `"goal"`) {
		t.Fatalf("updated=%+v encoded=%s err=%v", updated, encoded, err)
	}
	cleared := source.normalizeLocked(surface.StreamEvent{ID: "goal:2", ProviderKey: "goal:2", Kind: "goal", Operation: "replace"})
	clearedJSON, err := json.Marshal(cleared)
	if err != nil || !strings.Contains(string(clearedJSON), `"goal":null`) {
		t.Fatalf("cleared=%+v json=%s err=%v", cleared, clearedJSON, err)
	}
}

func TestSessionSourceSeedsBoundedTimelineBeforeStreaming(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
		items: []surface.TimelineItem{{
			ID:        "timeline-1",
			Kind:      "message",
			Text:      "persisted activity",
			Timestamp: "2026-10-03T12:00:00Z",
		}},
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	window, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Entries) != 1 {
		t.Fatalf("seeded entries=%d", len(window.Entries))
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(window.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ItemID != "timeline-1" || payload.ProviderKey != "timeline:timeline-1" || payload.Op != "upsert" || payload.Body != "persisted activity" {
		t.Fatalf("seed payload=%+v", payload)
	}
}

func TestSessionSourceSeedOverlapDoesNotRepublishOrAdvanceJournal(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	when := "2026-10-04T00:00:00Z"
	body := strings.Repeat("x", sessionStreamBodyBytes+512)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent, 2),
		items: []surface.TimelineItem{{
			ID:        "timeline-1",
			Kind:      "message",
			Role:      "assistant",
			Text:      body,
			Timestamp: when,
		}},
	}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	subscription, err := manager.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	seeded, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || len(seeded.Entries) != 1 || seeded.LatestSeq != 1 {
		t.Fatalf("seeded=%+v err=%v", seeded, err)
	}
	select {
	case entry := <-subscription.Entries:
		if entry.Seq != seeded.LatestSeq {
			t.Fatalf("seed event=%+v, want seq %d", entry, seeded.LatestSeq)
		}
	case <-time.After(time.Second):
		t.Fatal("seed event was not published")
	}
	adapter.events <- surface.StreamEvent{ID: "timeline-1", ProviderKey: "timeline:timeline-1", Version: uint64(len(body)), Operation: "upsert", Kind: "message", Role: "assistant", Text: body, Timestamp: timeMustParse(t, when)}
	select {
	case entry := <-subscription.Entries:
		t.Fatalf("identical overlap was published: %+v", entry)
	case <-time.After(150 * time.Millisecond):
	}
	unchanged, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || unchanged.LatestSeq != seeded.LatestSeq || len(unchanged.Entries) != 1 {
		t.Fatalf("unchanged=%+v err=%v", unchanged, err)
	}
	adapter.events <- surface.StreamEvent{ID: "timeline-1", ProviderKey: "timeline:timeline-1", Version: uint64(len(body) + 1), Operation: "upsert", Kind: "message", Role: "assistant", Text: body + "!", Timestamp: timeMustParse(t, when)}
	select {
	case entry := <-subscription.Entries:
		if entry.Seq != seeded.LatestSeq+1 {
			t.Fatalf("changed entry=%+v, want seq %d", entry, seeded.LatestSeq+1)
		}
	case <-time.After(time.Second):
		t.Fatal("changed overlap was not published")
	}
	changed, err := registry.SessionJournalAfter(from.ID, 0, 10)
	if err != nil || changed.LatestSeq != seeded.LatestSeq+1 || len(changed.Entries) != 1 {
		t.Fatalf("changed=%+v err=%v", changed, err)
	}
}

func TestSessionSourceSeedHandoffPreservesPrefixAndSuppressesDesktopReplay(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		items: []surface.TimelineItem{{
			ID:        "codex:turn-1:assistant:item-1",
			Kind:      "text",
			Role:      "assistant",
			Text:      "ha",
			Timestamp: "2026-10-04T00:00:00Z",
		}},
	}
	source := &sessionSource{manager: newSessionSourceManager(reg), session: from, adapter: adapter, epoch: "epoch", ctx: context.Background(), appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
	source.seedJournal()

	for _, event := range []surface.StreamEvent{
		{ID: "codex:turn-1:assistant:item-1", ProviderKey: "codex:turn-1:assistant:item-1", Cursor: 10, Version: 2, Operation: "append", Kind: "text", Role: "assistant", Text: "ha"},
		{ID: "codex:turn-1:assistant:item-1", ProviderKey: "codex:turn-1:assistant:item-1", Cursor: 9, Version: 2, Operation: "append", Kind: "text", Role: "assistant", Text: "ha"},
		{ID: "codex:turn-1:assistant:item-1", ProviderKey: "codex:turn-1:assistant:item-1", Cursor: 11, Version: 2, Operation: "append", Kind: "text", Role: "assistant", Text: "ha"},
	} {
		source.append(event)
	}

	page, err := reg.ReadSessionJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var payload sessionJournalPayload
	if err := json.Unmarshal(page.Entries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ProviderKey != "codex:turn-1:assistant:item-1" || payload.Body != "hahaha" || payload.Version != 2 {
		t.Fatalf("payload=%+v", payload)
	}
}

func timeMustParse(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestObservationHoldsActiveTurnSourceUntilIdle(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d.Surfaces = []surface.Surface{adapter}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	d.observeSession(context.Background(), adapter, &from)
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("active turn did not hold a source")
	}
	d.sourceHoldMu.Lock()
	_, held := d.sourceHolds[from.ID]["active-turn"]
	d.sourceHoldMu.Unlock()
	if !held {
		t.Fatal("active turn source was not held")
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	d.observeSession(context.Background(), adapter, &from)
	d.sourceHoldMu.Lock()
	_, held = d.sourceHolds[from.ID]["active-turn"]
	d.sourceHoldMu.Unlock()
	if held {
		t.Fatal("idle session source remained held")
	}
}

func TestSourceHoldsAreReferenceCountedByOwner(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{daemonSurface: fake, started: make(chan struct{}, 1), events: make(chan surface.StreamEvent)}
	adapter.caps.Stream = true
	d.Surfaces = []surface.Surface{adapter}
	d.setSessionSourceHold(&from, true, "active-turn")
	d.setSessionSourceHold(&from, true, "voice")
	d.setSessionSourceHold(&from, false, "voice")
	d.sourceHoldMu.Lock()
	_, active := d.sourceHolds[from.ID]["active-turn"]
	_, voice := d.sourceHolds[from.ID]["voice"]
	d.sourceHoldMu.Unlock()
	if !active || voice {
		t.Fatalf("active=%v voice=%v", active, voice)
	}
	d.setSessionSourceHold(&from, false, "active-turn")
}

func TestHeldSourceRestartsAfterUpstreamEnds(t *testing.T) {
	_, registry, fake, from, _ := daemonFixture(t)
	adapter := &restartingSource{daemonSurface: fake}
	adapter.caps.Stream = true
	manager := newSessionSourceManager(registry)
	release, err := manager.hold(&from, adapter, "active-turn")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	deadline := time.Now().Add(2 * time.Second)
	for adapter.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls := adapter.calls.Load(); calls < 2 {
		t.Fatalf("stream calls=%d", calls)
	}
}
