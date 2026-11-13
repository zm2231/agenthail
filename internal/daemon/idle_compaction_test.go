package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type idleSurface struct {
	*daemonSurface
	states map[string]surface.IdleCompactionState
}

func (s *idleSurface) IdleCompactionState(_ context.Context, sess *surface.Session, _ surface.IdleCompactionRead) (surface.IdleCompactionRead, error) {
	return surface.IdleCompactionRead{State: s.states[sess.ID], Offset: 1}, nil
}

func TestAnIdleLargeSessionIsCompactedOnceBeforeItsCacheExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	primary := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", primary)
	limited, off := filepath.Join(home, ".claude-limited"), filepath.Join(home, ".claude-off")
	if _, err := surface.WriteClaudeCompactSettings(home, off, surface.ClaudeCompactChange{AgenthailIdle: new(bool)}); err != nil {
		t.Fatal(err)
	}
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	now := time.Now()
	due := surface.IdleCompactionState{LastRequestAt: now.Add(-55 * time.Minute), OneHourCache: true, ContextTokens: 250_000}
	fake := &idleSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Compact: true}}, states: map[string]surface.IdleCompactionState{}}
	add := func(id, root string, state surface.IdleCompactionState, change func(*surface.Session)) surface.Session {
		t.Helper()
		transcript := filepath.Join(root, "projects", "work", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Transcript: transcript}
		if change != nil {
			change(&session)
		}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(session)
		if _, _, err := r.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		fake.sessions[id] = session
		fake.states[id] = state
		return session
	}
	with := func(change func(*surface.IdleCompactionState)) surface.IdleCompactionState {
		state := due
		change(&state)
		return state
	}
	add("due", primary, due, nil)
	add("early", primary, with(func(s *surface.IdleCompactionState) { s.LastRequestAt = now.Add(-40 * time.Minute) }), nil)
	add("expired", primary, with(func(s *surface.IdleCompactionState) { s.LastRequestAt = now.Add(-61 * time.Minute) }), nil)
	add("small", primary, with(func(s *surface.IdleCompactionState) { s.ContextTokens = 150_000 }), nil)
	add("five-minute-cache", primary, with(func(s *surface.IdleCompactionState) { s.OneHourCache = false }), nil)
	add("compacted", primary, with(func(s *surface.IdleCompactionState) { s.CompactedSince = true }), nil)
	add("busy", primary, due, func(s *surface.Session) { s.Status = surface.StatusBusy })
	add("background", primary, due, func(s *surface.Session) { s.BackgroundTasks = &surface.BackgroundTasks{Running: true} })
	add("queued", primary, due, nil)
	if err := r.QueueMessage("queued", "next instruction"); err != nil {
		t.Fatal(err)
	}
	add("under-a-limit", limited, due, nil)
	add("limited", limited, surface.IdleCompactionState{}, func(s *surface.Session) {
		s.UsageLimit = &surface.UsageLimit{ResetsAt: now.Add(2 * time.Hour)}
	})
	add("turned-off", off, due, nil)
	add("ended", primary, due, func(s *surface.Session) { s.Ended = true })

	d := newTestDaemon(t, r, []surface.Surface{fake})
	read := map[string]idleTranscript{}
	d.compactIdle(context.Background(), now, read)
	compacting := fake.sessions["due"]
	d.drainMessageQueue(context.Background(), fake, &compacting)
	testkit.Eventually(t, "the idle compact to run", 5*time.Second, func() bool { return fake.compactCalls.Load() == 1 && r.QueueCount("due") == 0 })
	d.compactIdle(context.Background(), now.Add(time.Minute), read)

	for id := range fake.sessions {
		history, err := r.ListHistory(50, id)
		if err != nil {
			t.Fatal(err)
		}
		queued := 0
		for _, entry := range history {
			if entry.Kind == "queued" && entry.Message == "idle compact" {
				queued++
			}
		}
		want := 0
		if id == "due" {
			want = 1
		}
		if queued != want {
			t.Errorf("%s: %d idle compactions queued, want %d", id, queued, want)
		}
	}

	response := serveDashboardRequest(dashboardRouter(d), http.MethodGet, "/api/v1/claude/compact-settings", "")
	var body struct {
		Holds []IdleCompactionHold `json:"idleCompactionHolds"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("compact settings = %d %s", response.Code, response.Body)
	}
	held := map[string]string{}
	for _, hold := range body.Holds {
		if _, twice := held[hold.SessionID]; twice {
			t.Errorf("%s held twice for one request", hold.SessionID)
		}
		held[hold.SessionID] = hold.Reason
	}
	wantHeld := map[string]string{
		"busy":          "the session is busy, not idle",
		"background":    "a background task is running",
		"queued":        "a message or command is queued or in flight",
		"under-a-limit": "the account under this root is at a usage limit",
		"turned-off":    "idle compaction is off for this root",
		"ended":         "the session has ended",
	}
	if !maps.Equal(held, wantHeld) {
		t.Errorf("held = %v, want %v", held, wantHeld)
	}
}

func TestEachHeldIdleCompactionIsReportedOnceWhenMoreAreHeldThanTheListKeeps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".claude")
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	now := time.Now()
	fake := &idleSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Compact: true}}, states: map[string]surface.IdleCompactionState{}}
	for i := range idleCompactionHoldsKept + 1 {
		id := fmt.Sprintf("busy-%02d", i)
		transcript := filepath.Join(root, "projects", "work", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusBusy, Transcript: transcript}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		fake.sessions[id] = session
		fake.states[id] = surface.IdleCompactionState{LastRequestAt: now.Add(-55 * time.Minute).Add(time.Duration(i) * time.Millisecond), OneHourCache: true, ContextTokens: 250_000}
	}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	router := dashboardRouter(d)
	holds := func() []IdleCompactionHold {
		t.Helper()
		response := serveDashboardRequest(router, http.MethodGet, "/api/v1/claude/compact-settings", "")
		var body struct {
			Holds []IdleCompactionHold `json:"idleCompactionHolds"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("compact settings = %d %s", response.Code, response.Body)
		}
		return body.Holds
	}
	read := map[string]idleTranscript{}
	d.compactIdle(context.Background(), now, read)
	first := holds()
	d.compactIdle(context.Background(), now.Add(time.Minute), read)
	if second := holds(); len(first) != idleCompactionHoldsKept || !slices.EqualFunc(first, second, func(a, b IdleCompactionHold) bool { return a.SessionID == b.SessionID && a.At.Equal(b.At) }) {
		t.Fatalf("holds after the first pass: %d, after the second they changed: %v", len(first), second)
	}
}
