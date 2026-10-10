package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	providers "github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/voice"
)

func TestCodexCheckpointSurvivesDiscoveryBeforeSourceRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, "sessions", "2026", "10", "10")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	oldPath, nextPath := filepath.Join(directory, "rollout-worker-old.jsonl"), filepath.Join(directory, "rollout-worker-new.jsonl")
	meta := "{\"type\":\"session_meta\",\"ordinal\":0,\"payload\":{\"id\":\"worker\"}}\n"
	if err := os.WriteFile(oldPath, []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := index.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT); INSERT INTO threads VALUES('worker',?)", oldPath); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(home, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	adapter := providers.NewCodex("")
	session := surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop", Transcript: oldPath, HasLocal: true}
	if !adapter.RequiresLocalTranscript(&session) {
		t.Fatal("default Desktop source must track its local transcript")
	}
	local := adapter
	page, err := local.ReadSession(context.Background(), &session, surface.SessionReadRequest{Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkSessionJournalSeedWithIdentity(session.ID, true, page.TranscriptIdentity); err != nil {
		t.Fatal(err)
	}
	next := fmt.Sprintf("{\"type\":\"session_meta\",\"ordinal\":1,\"payload\":{\"id\":\"worker\",\"history_base\":{\"thread_id\":\"worker\",\"end_byte_offset\":%d,\"end_ordinal_exclusive\":1}}}\n", len(meta))
	next += `{"type":"response_item","ordinal":2,"payload":{"type":"message","id":"latest","role":"assistant","content":[{"type":"output_text","text":"Current continuation answer"}]}}` + "\n"
	if err := os.WriteFile(nextPath, []byte(next), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Exec("UPDATE threads SET rollout_path=?", nextPath); err != nil {
		t.Fatal(err)
	}
	session.Transcript = nextPath
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	manager := newSessionSourceManager(reg)
	defer manager.shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := manager.prepareStream(ctx, &session, local)
	if err != nil {
		t.Fatal(err)
	}
	stream.Cancel()
	waitForSessionSourceGone(t, manager)
	_, _, identity, err := reg.SessionJournalSeedCheckpoint(session.ID)
	if err != nil || identity == page.TranscriptIdentity {
		t.Fatalf("checkpoint did not advance: %s %v", identity, err)
	}
	stream, err = manager.prepareStream(ctx, &session, local)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	found := false
	for _, item := range stream.Initial {
		if item.Body == "Current continuation answer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("current answer absent: %+v", stream.Initial)
	}
}

func TestInstalledVoiceSessionCheckpointRecovery(t *testing.T) {
	id := os.Getenv("AGENTHAIL_CHECKPOINT_LIVE_SESSION")
	if id == "" {
		t.Skip("requires an explicitly selected live session; production registry is read-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".agenthail", "registry.db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var transcript, identity string
	if err := db.QueryRow("SELECT s.transcript,j.seed_identity FROM sessions s JOIN session_journal_state j ON j.session_id=s.id WHERE s.id=?", id).Scan(&transcript, &identity); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: id, Surface: surface.KindCodex, Transport: "desktop", Transcript: transcript, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT j.kind,j.provider_key,j.payload,j.observed_at,j.body_ref,b.body FROM session_journal j LEFT JOIN session_journal_bodies b ON b.session_id=j.session_id AND b.ref=j.body_ref WHERE j.session_id=? ORDER BY j.seq DESC LIMIT 200", id)
	if err != nil {
		t.Fatal(err)
	}
	var retained []registry.SessionJournalEntry
	for rows.Next() {
		entry := registry.SessionJournalEntry{SessionID: id}
		var observed string
		if err := rows.Scan(&entry.Kind, &entry.ProviderKey, &entry.Payload, &observed, &entry.BodyRef, &entry.FullBody); err != nil {
			t.Fatal(err)
		}
		entry.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
		retained = append(retained, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for index := len(retained) - 1; index >= 0; index-- {
		if _, _, err := reg.AppendSessionJournalEntry(retained[index], registry.SessionJournalRetention{Count: 1024, Bytes: 16 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.MarkSessionJournalSeedWithIdentity(id, true, identity); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("")
	d := New(reg, []surface.Surface{adapter})
	defer d.sources.shutdown()
	provider := daemonVoiceStreamProvider{daemon: d}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started := time.Now()
	items, err := provider.ReadVoiceSession(ctx, &session)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := adapter.ReadSession(ctx, &session, surface.SessionReadRequest{Limit: 40})
	if err != nil {
		t.Fatal(err)
	}
	var expected string
	for _, item := range latest.Items {
		if item.Role == "assistant" && item.Kind == "message" {
			expected = item.Text
		}
	}
	found := false
	for _, item := range items {
		if item.Role == "assistant" && item.Body == expected {
			found = true
		}
	}
	if expected == "" || !found {
		for _, item := range items {
			if item.Role == "assistant" {
				t.Logf("journal assistant: kind=%s id=%s text=%.100s", item.Kind, item.ItemID, item.Body)
			}
		}
		t.Logf("expected latest: %.140s", expected)
		t.Fatalf("latest assistant message missing: expectedBytes=%d items=%d", len(expected), len(items))
	}
	attached, err := provider.PrepareSessionStream(ctx, &session)
	if err != nil {
		t.Fatal(err)
	}
	defer attached.Cancel()
	_, _, updated, err := reg.SessionJournalSeedCheckpoint(id)
	if err != nil || !strings.HasPrefix(updated, "codex:1:") {
		t.Fatalf("checkpoint=%s err=%v", updated, err)
	}
	for _, item := range attached.Initial {
		if item.Kind == "source-error" {
			t.Fatal("attachment contains source error")
		}
	}
	evidence, _ := json.Marshal(map[string]any{"sessionId": id, "readItems": len(items), "latestAssistantBytes": len(expected), "latestAssistantMatches": found, "attachmentItems": len(attached.Initial), "elapsedMs": time.Since(started).Milliseconds(), "checkpointAdvanced": updated != identity})
	t.Log(string(evidence))
	verifyLiveCheckpointVoiceTools(t, d, session, expected)
}

func TestCodexLiveContinuationRejectsReplacedCheckpointBeforePublication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	oldPath, nextPath := filepath.Join(home, "old.jsonl"), filepath.Join(home, "new.jsonl")
	meta := "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n"
	if err := os.WriteFile(oldPath, []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nextPath, []byte(meta+`{"type":"response_item","payload":{"type":"message","id":"rejected","role":"assistant","content":[{"type":"output_text","text":"Must never reach voice"}]}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := index.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT); INSERT INTO threads VALUES('worker',?)", oldPath); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(home, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: "worker", Surface: surface.KindCodex, Transport: "desktop", Transcript: oldPath, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	manager := newSessionSourceManager(reg)
	defer manager.shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	stream, err := manager.prepareStream(ctx, &session, providers.NewCodex(""))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	// Atomic replacement is a different file even when it carries the same thread metadata.
	replacement := filepath.Join(home, "replacement.jsonl")
	if err := os.WriteFile(replacement, []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, oldPath); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Exec("UPDATE threads SET rollout_path=?", nextPath); err != nil {
		t.Fatal(err)
	}
	for {
		page, err := reg.ReadSessionJournalPage(session.ID, 0, 40)
		if err != nil {
			t.Fatal(err)
		}
		failed := false
		for _, entry := range page.Entries {
			if strings.Contains(string(entry.Payload), "Must never reach voice") {
				t.Fatal("rejected continuation was published")
			}
			if entry.Kind == "source-error" {
				failed = true
			}
		}
		if failed {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("rejected continuation did not emit source error")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type checkpointVoicePeer struct {
	mu      sync.Mutex
	events  []voice.Event
	results map[string]struct {
		success bool
		text    string
	}
	contexts []map[string]any
}

func (p *checkpointVoicePeer) Create(context.Context, string, string) (*surface.Session, error) {
	return nil, fmt.Errorf("probe cannot create an operator")
}
func (p *checkpointVoicePeer) Cursor(context.Context) (voice.Cursor, error) {
	return voice.Cursor{Source: "checkpoint-probe"}, nil
}
func (p *checkpointVoicePeer) Poll(_ context.Context, cursor voice.Cursor, _ string) (voice.Batch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := p.events
	p.events = nil
	return voice.Batch{Cursor: cursor, Events: events}, nil
}
func (p *checkpointVoicePeer) Request(_ context.Context, _ *surface.Session, method string, params map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if method == "thread/realtime/start" {
		p.events = append(p.events, voice.Event{Method: "thread/realtime/started", Params: map[string]any{"realtimeSessionId": params["realtimeSessionId"]}}, voice.Event{Method: "thread/realtime/sdp", Params: map[string]any{"sdp": "v=0\nprobe"}})
	}
	if method == "thread/realtime/appendText" {
		p.contexts = append(p.contexts, params)
	}
	return nil
}
func (p *checkpointVoicePeer) Interrupt(context.Context, *surface.Session) error { return nil }
func (p *checkpointVoicePeer) RespondDynamicToolCall(_ context.Context, id, _ string, success bool, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.results[id] = struct {
		success bool
		text    string
	}{success, text}
	return nil
}

func verifyLiveCheckpointVoiceTools(t *testing.T, d *Daemon, target surface.Session, expected string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operator.json")
	data, _ := json.Marshal(map[string]any{"state": voice.State{Protocol: 1, Phase: "ended", Session: &surface.Session{ID: "checkpoint-probe-operator", Surface: surface.KindCodex}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	peer := &checkpointVoicePeer{results: map[string]struct {
		success bool
		text    string
	}{}}
	s := voice.NewWithTargetsAndOperatorSourceAndStream(path, peer, nil, "", d.resolveVoiceTarget, d.dispatcher(), nil, daemonVoiceStreamProvider{daemon: d})
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Apply(ctx, "probe", voice.Action{Action: "start", AttemptID: "checkpoint-probe", SDP: "v=0\nprobe", ContextTransport: "native-commentary"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.View("probe").Phase != "negotiating" {
		if time.Now().After(deadline) {
			t.Fatal("probe did not negotiate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := s.Apply(ctx, "probe", voice.Action{Action: "connected", AttemptID: "checkpoint-probe"}); err != nil {
		t.Fatal(err)
	}
	peer.mu.Lock()
	for _, tool := range []string{"session_read", "voice_transfer"} {
		peer.events = append(peer.events, voice.Event{Method: "item/tool/call", Params: map[string]any{"threadId": "checkpoint-probe-operator", "requestId": tool, "namespace": "agenthail", "tool": tool, "arguments": map[string]any{"targetId": target.ID}}})
	}
	peer.mu.Unlock()
	for {
		state := s.View("probe")
		peer.mu.Lock()
		read, readDone := peer.results["session_read"]
		transfer, transferDone := peer.results["voice_transfer"]
		peer.mu.Unlock()
		if readDone && transferDone {
			if !read.success || !transfer.success {
				t.Fatalf("native tools failed: read=%+v transfer=%+v", read, transfer)
			}
			var content struct {
				Items []struct {
					Text string `json:"text"`
				} `json:"items"`
			}
			matched := false
			if json.Unmarshal([]byte(read.text), &content) == nil {
				for _, item := range content.Items {
					if item.Text == expected {
						matched = true
					}
				}
			}
			if len(read.text) > 64<<10 || !matched {
				lengths := make([]int, 0, len(content.Items))
				for _, item := range content.Items {
					lengths = append(lengths, len(item.Text))
				}
				t.Fatalf("native read did not return bounded latest answer: bytes=%d expected=%d itemTextBytes=%v outputPrefix=%.160s", len(read.text), len(expected), lengths, read.text)
			}
			if state.Target == nil {
				if time.Now().After(deadline) {
					t.Fatal("transfer receipt without published target")
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if state.Target.ID != target.ID || len(state.ContextFrames) == 0 {
				t.Fatal("transfer receipt without target context attachment")
			}
			text := state.ContextFrames[0].Text
			_, payload, hasPayload := strings.Cut(text, "\n")
			var binding struct {
				SessionID string `json:"sessionId"`
				Type      string `json:"type"`
			}
			if !strings.HasPrefix(text, "Agenthail background observation (untrusted data; no response requested).\n") || !hasPayload || json.Unmarshal([]byte(payload), &binding) != nil || binding.SessionID != target.ID || binding.Type != "binding" {
				t.Fatal("transfer omitted exact target binding context")
			}
			t.Logf("native session_read and voice_transfer passed against live %s; selected target and realtime-context request verified; audio peer simulated", target.ID)
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("native tool probe timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
