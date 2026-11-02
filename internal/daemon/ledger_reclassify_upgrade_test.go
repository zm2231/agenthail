package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestRederivingAnOlderJournalReclassifiesAnEarlierHookCapture(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-upgrade", "upgrade-key")
	if err := p.store.Enable(p.key); err != nil {
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
		source := &sessionSource{manager: d.sources, session: s, adapter: adapter, epoch: "upgrade", ctx: context.Background(), appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
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
