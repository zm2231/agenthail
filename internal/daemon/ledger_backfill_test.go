package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func journalSource(d *Daemon, session surface.Session, adapter surface.Surface) *sessionSource {
	return &sessionSource{manager: d.sources, session: session, adapter: adapter, epoch: "test", appendBodies: map[string]string{}, followers: map[uint64]chan struct{}{}}
}

func typedPrompt(id, promptID, text string, at time.Time) surface.TimelineItem {
	return surface.TimelineItem{ID: id, Kind: "message", Role: "user", Text: text, MessageID: id, PromptID: promptID, Timestamp: at.UTC().Format(time.RFC3339Nano)}
}

func liveEvent(item surface.TimelineItem) surface.StreamEvent {
	at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
	return surface.StreamEvent{ID: item.ID, ProviderKey: "timeline:" + item.ID, Operation: "upsert", Kind: item.Kind, Role: item.Role, Origin: item.Origin, Text: item.Text, MessageID: item.MessageID, PromptID: item.PromptID, Timestamp: at}
}

func TestDashboardSendAndItsJournalEntryAreOneUtterance(t *testing.T) {
	r := openLedgerRegistry(t)
	p := addLedgerProject(t, r, "proj-send", "send-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	target := p.session(t, r, "target")
	fake := &daemonSurface{sessions: map[string]surface.Session{"target": target}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	receipt, err := d.dispatcher().DeliverWithOptions(context.Background(), fake, &target, "ship the board", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil || receipt.MessageID == "" {
		t.Fatalf("person send: receipt %+v err %v", receipt, err)
	}
	journalSource(d, target, fake).append(liveEvent(surface.TimelineItem{ID: "peer:" + receipt.MessageID, Kind: "message", Role: "user", Origin: surface.OriginPerson, Text: "ship the board", MessageID: receipt.MessageID, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}))
	events := p.events(t)
	if len(events) != 1 || events[0].Ref != ledger.DeliveryRef(receipt.MessageID) {
		t.Fatalf("want one utterance for the send and its journal entry, got %+v", events)
	}
}

func TestPersonSendToTheCoordinatorIsCapturedInUnsetProject(t *testing.T) {
	r := openLedgerRegistry(t)
	p := addLedgerProject(t, r, "proj-unset", "unset-send-key")
	coordinator := p.session(t, r, "coord")
	worker := p.session(t, r, "worker")
	makeCoordinator(t, r, "proj-unset", p, "coord")
	fake := &daemonSurface{sessions: map[string]surface.Session{"coord": coordinator, "worker": worker}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	for _, target := range []surface.Session{coordinator, worker} {
		if _, err := d.dispatcher().DeliverWithOptions(context.Background(), fake, &target, "to "+target.ID, "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
			t.Fatal(err)
		}
	}
	events := p.events(t)
	if len(events) != 1 || events[0].ActorSessionID != surface.PersonSessionID {
		t.Fatalf("want only the send to the coordinator, got %+v", events)
	}
}

func TestTypedPromptHookAndJournalAreOneUtterancePerSubmission(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-typed", "typed-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "s-typed")
	source := journalSource(d, s, nil)
	now := time.Now()
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "yes"})
	source.append(liveEvent(typedPrompt("u-1", "p-1", "yes", now)))
	source.append(liveEvent(typedPrompt("u-2", "p-2", "yes", now.Add(time.Minute))))
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-2", Prompt: "yes"})
	events := p.events(t)
	if len(events) != 2 || events[0].Ref != ledger.PromptRef("p-1") || events[1].Ref != ledger.PromptRef("p-2") {
		t.Fatalf("want two utterances, one per submission, got %+v", events)
	}
}

func TestBackfillSkipsInjectedAndAgentMessages(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-agent", "agent-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "s-agent")
	source := journalSource(d, s, nil)
	now := time.Now()
	source.append(liveEvent(surface.TimelineItem{ID: "peer:x", Kind: "message", Role: "peer", Text: "do it", MessageID: "x", Timestamp: now.Format(time.RFC3339Nano)}))
	source.append(liveEvent(surface.TimelineItem{ID: "task", Kind: "message", Role: "user", Origin: surface.OriginTaskNotification, Text: "task done", PromptID: "p-t", Timestamp: now.Format(time.RFC3339Nano)}))
	source.append(liveEvent(surface.TimelineItem{ID: "reply", Kind: "message", Role: "assistant", Text: "ok", Timestamp: now.Format(time.RFC3339Nano)}))
	if events := p.events(t); len(events) != 0 {
		t.Fatalf("agent and injected messages recorded as the person's words: %+v", events)
	}
}

func TestBackfillHonoursTheProjectGrant(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-grant", "grant-key")
	s := p.session(t, r, "s-grant")
	source := journalSource(d, s, nil)
	before := time.Now().Add(-time.Hour)
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	enabledAt := time.Now()
	source.appendSeedItems([]surface.TimelineItem{typedPrompt("old", "p-old", "from before the ledger", before), typedPrompt("new", "p-new", "after it", enabledAt.Add(time.Second))})
	if events := p.events(t); len(events) != 1 || events[0].Ref != ledger.PromptRef("p-new") {
		t.Fatalf("want only the prompt after enable, got %+v", events)
	}

	if err := p.store.Disable(p.key); err != nil {
		t.Fatal(err)
	}
	gap := time.Now()
	time.Sleep(5 * time.Millisecond)
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	source.appendSeedItems([]surface.TimelineItem{typedPrompt("gap", "p-gap", "while disabled", gap)})
	if events := p.events(t); len(events) != 1 {
		t.Fatalf("a prompt from the disabled gap was recorded: %+v", events)
	}
}

func TestBackfillStartsTheCoordinatorAtItsAssignment(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-coord", "coord-key")
	s := p.session(t, r, "late-coordinator")
	source := journalSource(d, s, nil)
	history := time.Now().Add(-time.Hour)
	makeCoordinator(t, r, "proj-coord", p, s.ID)
	source.appendSeedItems([]surface.TimelineItem{typedPrompt("old", "p-old", "older history", history), typedPrompt("new", "p-new", "as coordinator", time.Now().Add(time.Second))})
	if events := p.events(t); len(events) != 1 || events[0].Ref != ledger.PromptRef("p-new") {
		t.Fatalf("want only prompts after the session became coordinator, got %+v", events)
	}
}

func openLedgerRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
