package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const managedRolloutStart = `{"type":"session_meta","payload":{"id":"managed-thread","cwd":"/work"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"type":"response_item","payload":{"id":"prompt","type":"message","role":"user","content":[{"type":"input_text","text":"first prompt"}]}}
`

const managedRolloutSteer = `{"type":"response_item","payload":{"id":"steer","type":"message","role":"user","content":[{"type":"input_text","text":"steered prompt"}]}}
{"type":"response_item","payload":{"id":"m2","type":"message","role":"assistant","content":[{"type":"output_text","text":"ok 2"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}
`

func managedTurnAppServer(t *testing.T, finished *atomic.Bool) {
	t.Helper()
	codextest.Start(t, func(method string, _ map[string]any) map[string]any {
		turn := map[string]any{"id": "turn-1", "status": "inProgress", "items": []any{}}
		if finished.Load() {
			turn = map[string]any{"id": "turn-1", "status": "completed", "items": []any{map[string]any{"id": "m2", "type": "agentMessage", "phase": "final_answer", "text": "ok 2"}}}
		}
		switch method {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": "managed-thread", "turns": []any{turn}}}
		case "thread/turns/list":
			return map[string]any{"data": []any{turn}}
		}
		return nil
	})
}

func managedCodexSession(t *testing.T, reg *registry.Registry, rollout string) surface.Session {
	t.Helper()
	transcript := filepath.Join(t.TempDir(), "rollout-managed-thread.jsonl")
	if err := os.WriteFile(transcript, []byte(rollout), 0600); err != nil {
		t.Fatal(err)
	}
	session := surface.Session{ID: "managed-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusBusy, Source: "agenthail", Transport: "managed", Transcript: transcript, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	return session
}

func journalBodies(t *testing.T, reg *registry.Registry, sessionID string) (map[string]int, []string) {
	t.Helper()
	page, err := reg.ReadSessionJournalPage(sessionID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	bodies := map[string]int{}
	var keys []string
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Op == "remove" {
			continue
		}
		bodies[payload.Body]++
		keys = append(keys, payload.ProviderKey)
	}
	return bodies, keys
}

func awaitJournalBody(t *testing.T, reg *registry.Registry, sessionID, body string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if bodies, _ := journalBodies(t, reg, sessionID); bodies[body] > 0 {
			return
		}
		if time.Now().After(deadline) {
			bodies, keys := journalBodies(t, reg, sessionID)
			t.Fatalf("journal never held %q: bodies=%v keys=%v", body, bodies, keys)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertManagedTurnOnce(t *testing.T, reg *registry.Registry, sessionID string) {
	t.Helper()
	bodies, keys := journalBodies(t, reg, sessionID)
	for _, body := range []string{"first prompt", "steered prompt", "ok 2"} {
		if bodies[body] != 1 {
			t.Fatalf("body %q count=%d keys=%v", body, bodies[body], keys)
		}
	}
	for _, key := range keys {
		if strings.HasPrefix(key, "managed:") {
			t.Fatalf("journal kept a second key space: %v", keys)
		}
	}
}

func TestAManagedCodexTurnJournalsAPromptSteeredAfterItsSeedOnce(t *testing.T) {
	var finished atomic.Bool
	managedTurnAppServer(t, &finished)
	_, reg, _, _, _ := daemonFixture(t)
	session := managedCodexSession(t, reg, managedRolloutStart)
	adapter := surfaces.NewCodex("1")
	manager := newTestSourceManager(t, reg)
	first, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	awaitJournalBody(t, reg, session.ID, "first prompt")
	if err := appendFile(t, session.Transcript, managedRolloutSteer); err != nil {
		t.Fatal(err)
	}
	finished.Store(true)
	awaitJournalBody(t, reg, session.ID, "ok 2")
	awaitJournalBody(t, reg, session.ID, "steered prompt")
	assertManagedTurnOnce(t, reg, session.ID)

	first.Cancel()
	waitForSessionSourceGone(t, manager)
	if err := reg.MarkSessionJournalSeed(session.ID, false); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	assertManagedTurnOnce(t, reg, session.ID)
}

func TestAManagedCodexThreadWithoutARolloutYetJournalsItsFirstTurnWhenItStarts(t *testing.T) {
	var finished atomic.Bool
	finished.Store(true)
	managedTurnAppServer(t, &finished)
	_, reg, _, _, _ := daemonFixture(t)
	now := time.Now()
	millis := fmt.Sprintf("%012x", now.UnixMilli())
	session := surface.Session{ID: millis[:8] + "-" + millis[8:] + "-7000-8000-000000000001", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle, Source: "agenthail", Transport: "managed", HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	manager := newTestSourceManager(t, reg)
	subscription, err := manager.prepareStream(context.Background(), &session, surfaces.NewCodex("1"))
	if err != nil {
		t.Fatalf("a thread whose first turn has not started could not be followed: %v", err)
	}
	defer subscription.Cancel()
	rollout := filepath.Join(os.Getenv("CODEX_HOME"), "sessions", now.Format("2006/01/02"), "rollout-"+now.Format("2006-01-02T15-04-05")+"-"+session.ID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte(strings.ReplaceAll(managedRolloutStart+managedRolloutSteer, "managed-thread", session.ID)), 0600); err != nil {
		t.Fatal(err)
	}
	awaitJournalBody(t, reg, session.ID, "ok 2")
	assertManagedTurnOnce(t, reg, session.ID)
	_, keys := journalBodies(t, reg, session.ID)
	for _, key := range keys {
		if strings.HasSuffix(key, ":source") {
			t.Fatalf("waiting for the rollout reported a source error: %v", keys)
		}
	}
}

func TestAManagedCodexJournalWithAppServerRowsIsReadOnceFromItsRollout(t *testing.T) {
	var finished atomic.Bool
	finished.Store(true)
	managedTurnAppServer(t, &finished)
	_, reg, _, _, _ := daemonFixture(t)
	session := managedCodexSession(t, reg, managedRolloutStart+managedRolloutSteer)
	adapter := surfaces.NewCodex("1")
	manager := newTestSourceManager(t, reg)
	first, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	awaitJournalBody(t, reg, session.ID, "ok 2")
	first.Cancel()
	waitForSessionSourceGone(t, manager)
	retention := registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}
	for _, payload := range []sessionJournalPayload{
		{ItemID: "managed:turn-1:text", ProviderKey: "managed:turn-1:text", Version: 4, Op: "upsert", Kind: "text", TurnID: "turn-1", Body: "ok 2"},
		{ItemID: "managed:turn-1:done:phase:done", ProviderKey: "managed:turn-1:done:phase:done", Version: 4, Op: "phase", Kind: "done", TurnID: "turn-1"},
	} {
		encoded, _ := json.Marshal(payload)
		if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: session.ID, Kind: payload.Kind, ProviderKey: payload.ProviderKey, Payload: encoded, ObservedAt: time.Now().UTC()}, retention); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.MarkSessionJournalDerivation(session.ID, 1); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	assertManagedTurnOnce(t, reg, session.ID)
}
