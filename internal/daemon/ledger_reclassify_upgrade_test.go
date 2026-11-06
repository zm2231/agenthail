package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASessionWithNoHookCaptureSkipsTheRederive(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	s := surface.Session{ID: "quiet", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle, Transcript: "/transcript.jsonl"}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(sessionJournalPayload{ItemID: "100-aaaaaa-0", Op: "upsert", Version: 1, ProviderKey: "timeline:100-aaaaaa-0", Kind: "message", Role: "user", Body: "hello"})
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.ID, Kind: "message", ProviderKey: "timeline:100-aaaaaa-0", Payload: payload}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []error{r.MarkSessionJournalSeedWithIdentity(s.ID, true, "transcript"), r.MarkSessionJournalDerivation(s.ID, 1), r.SetSessionTranscriptCheckpoint(s.ID, registry.TranscriptCheckpoint{Offset: 900, Identity: "transcript"})} {
		if step != nil {
			t.Fatal(step)
		}
	}
	before, err := r.ReadSessionJournalPage(s.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{sessions: map[string]surface.Session{s.ID: s}, observations: map[string]*surface.TurnObservation{}, accepted: true, kind: surface.KindClaude}
	adapter := &rederiveSurface{daemonSurface: fake, items: []surface.TimelineItem{{ID: "100-aaaaaa-0", Kind: "message", Role: "user", Text: "hello"}}}
	source := &sessionSource{manager: d.sources, session: s, adapter: adapter, epoch: "quiet", ctx: context.Background(), appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
	if err := source.loadSeed(); err != nil {
		t.Fatal(err)
	}
	after, err := r.ReadSessionJournalPage(s.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	version, err := r.SessionJournalDerivation(s.ID)
	if err != nil || adapter.calls != 0 || after.LatestSeq != before.LatestSeq || version != transcriptJournalDerivation {
		t.Fatalf("read the transcript %d times, latest %d then %d, derivation %d %v", adapter.calls, before.LatestSeq, after.LatestSeq, version, err)
	}
}

func TestRederivesWaitForASlot(t *testing.T) {
	r := openLedgerRegistry(t)
	manager := newTestSourceManager(t, r)
	s := surface.Session{ID: "waiting", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: "/transcript.jsonl"}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	for range journalRederives {
		manager.rederives <- struct{}{}
	}
	fake := &daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, kind: surface.KindClaude}
	adapter := &rederiveSurface{daemonSurface: fake}
	source := &sessionSource{manager: manager, session: s, adapter: adapter, epoch: "waiting", appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := source.rederiveJournal(ctx); !errors.Is(err, context.DeadlineExceeded) || adapter.calls != 0 {
		t.Fatalf("a rederive past the bound ran: %v, %d reads", err, adapter.calls)
	}
}

func TestRederivingAnOlderJournalReclassifiesAnEarlierHookCapture(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-upgrade", "upgrade-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	s := surface.Session{ID: "upgraded", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: p.dir, Status: surface.StatusIdle, Transcript: "/transcript.jsonl"}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []ledgerCaptureRequest{{SessionID: s.ID, PromptID: "wake-old", Prompt: "Check the verdict; continue the loop."}, {SessionID: s.ID, PromptID: "typed-old", Prompt: "ship it"}} {
		if !ledgerPost(t, handler, prompt).Captured {
			t.Fatalf("the earlier daemon's capture of %s", prompt.PromptID)
		}
	}

	appendRow := func(payload sessionJournalPayload) {
		payload.Op, payload.Version, payload.ProviderKey = "upsert", 1, "timeline:"+payload.ItemID
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	appendRow(sessionJournalPayload{ItemID: "100-aaaaaa-0", Kind: "message", Role: "user", Body: "ship it", PromptID: "typed-old", TS: "2026-11-02T10:00:00Z"})
	appendRow(sessionJournalPayload{ItemID: "500-eeeeee-0", Kind: "event", Title: "Claude Code instructions", TS: "2026-11-02T12:57:34Z"})
	if err := r.MarkSessionJournalSeedWithIdentity(s.ID, true, "transcript"); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkSessionJournalDerivation(s.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.SetSessionTranscriptCheckpoint(s.ID, registry.TranscriptCheckpoint{Offset: 900, Identity: "transcript"}); err != nil {
		t.Fatal(err)
	}

	fake := &daemonSurface{sessions: map[string]surface.Session{s.ID: s}, observations: map[string]*surface.TurnObservation{}, accepted: true, kind: surface.KindClaude}
	adapter := &rederiveSurface{daemonSurface: fake, items: []surface.TimelineItem{
		{ID: "100-aaaaaa-0", Kind: "message", Role: "user", Text: "ship it", PromptID: "typed-old", Timestamp: "2026-11-02T10:00:00Z"},
		{ID: "500-eeeeee-0", Kind: "event", Title: "Claude Code instructions", PromptID: "wake-old", Timestamp: "2026-11-02T12:57:34Z"},
	}}
	for range 2 {
		source := &sessionSource{manager: d.sources, session: s, adapter: adapter, epoch: "upgrade", ctx: context.Background(), appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
		if err := source.loadSeed(); err != nil {
			t.Fatal(err)
		}
	}

	var reclassified []string
	for _, e := range p.events(t) {
		if e.Type == ledger.EventReclassified {
			reclassified = append(reclassified, e.Ref)
		}
	}
	if len(reclassified) != 1 || reclassified[0] != "reclassified:"+ledger.PromptRef("wake-old") {
		t.Fatalf("want one reclassification of the wakeup, got %v", reclassified)
	}
	unattached, err := p.store.UnattachedFor(p.key, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unattached) != 1 || unattached[0].ID != ledger.PromptRef("typed-old") {
		t.Fatalf("person words after the upgrade: %+v", unattached)
	}
}
