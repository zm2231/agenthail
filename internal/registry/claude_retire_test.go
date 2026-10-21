package registry

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeRowThatNeverHeldAConversationIsRemovedWithItsCatalogRow(t *testing.T) {
	r := openTestRegistry(t)
	record := func(session surface.Session) {
		t.Helper()
		session.Surface, session.RuntimeKind = surface.KindClaude, surface.RuntimeClaudeCLI
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"` + session.ID + `"}`}, CatalogEvent{DedupeKey: session.ID + ":upsert", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	record(surface.Session{ID: "never-written", PID: 4102, Bridge: "session_left", Status: surface.StatusIdle})
	record(surface.Session{ID: "written", Transcript: "/fixture/written.jsonl", HasLocal: true, Status: surface.StatusIdle})
	record(surface.Session{ID: "named", Status: surface.StatusIdle})
	record(surface.Session{ID: "queued", Status: surface.StatusIdle})
	if err := r.SetAlias("kept", "named"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions("queued", "waiting for a process", "", surface.SendOptions{BusyDelivery: "queue"}); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if _, err := r.RecordCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, time.Now(), true, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Session("never-written"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a row that never held a conversation is still registered: err=%v", err)
	}
	for _, kept := range []string{"written", "named", "queued"} {
		if session, err := r.Session(kept); err != nil || session.Status != surface.StatusOffline {
			t.Fatalf("%s=%+v err=%v, want it kept offline", kept, session, err)
		}
	}
}

func TestRegisteringASharedConversationDetachesEachHolderFromItsEarlierRow(t *testing.T) {
	r := openTestRegistry(t)
	claude := func(session surface.Session) surface.Session {
		session.Surface, session.RuntimeKind = surface.KindClaude, surface.RuntimeClaudeCLI
		return session
	}
	if err := r.RegisterSession(claude(surface.Session{ID: "started", PID: 4102, Bridge: "session_moved", Transport: "uds"})); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(claude(surface.Session{ID: "shared", Holders: []surface.Holder{{PID: 4101}, {PID: 4102}}})); err != nil {
		t.Fatal(err)
	}
	started, err := r.Session("started")
	if err != nil || started.PID != 0 || started.Bridge != "" {
		t.Fatalf("started=%+v err=%v, want the process detached from the row it left", started, err)
	}
	shared, err := r.Session("shared")
	if err != nil || shared.PID != 0 || shared.Bridge != "" {
		t.Fatalf("shared=%+v err=%v, want no single process attached", shared, err)
	}
}

func TestASessionAddressedToOneHolderLeavesTheConversationRowAsDiscovered(t *testing.T) {
	r := openTestRegistry(t)
	shared := surface.Session{ID: "shared", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Holders: []surface.Holder{{PID: 4101}, {PID: 4102}}}
	if err := r.RegisterSession(shared); err != nil {
		t.Fatal(err)
	}
	pinned := surface.Session{ID: "shared", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: 4102, HolderPID: 4102, Transport: "uds"}
	if err := r.RegisterSession(pinned); err != nil {
		t.Fatal(err)
	}
	if row, err := r.Session("shared"); err != nil || row.PID != 0 {
		t.Fatalf("row=%+v err=%v, want no single process bound by a pinned write", row, err)
	}
}

func TestASessionAddressedToOneHolderOfAnUnlistedConversationGetsARowWithNoProcess(t *testing.T) {
	r := openTestRegistry(t)
	pinned := surface.Session{ID: "unlisted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: 4102, HolderPID: 4102, Transport: "uds"}
	if err := r.RegisterSession(pinned); err != nil {
		t.Fatal(err)
	}
	if row, err := r.Session("unlisted"); err != nil || row.PID != 0 {
		t.Fatalf("row=%+v err=%v, want a row with no process bound", row, err)
	}
}
