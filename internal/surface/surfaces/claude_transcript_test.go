package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func writeTranscript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeListOmitsDeadBridgeProcesses(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	active := `{"bridgeSessionId":"active","pid":` + fmt.Sprint(os.Getpid()) + `,"status":"idle"}`
	stale := `{"bridgeSessionId":"stale","pid":2147483647,"status":"idle"}`
	if err := os.WriteFile(filepath.Join(dir, "active.json"), []byte(active), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.json"), []byte(stale), 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := NewClaude("", home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "active" {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeListUsesTranscriptTurnState(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		t.Fatal(err)
	}
	bridge := `{"bridgeSessionId":"bridge","sessionId":"local","cwd":` + fmt.Sprintf("%q", cwd) + `,"pid":` + fmt.Sprint(os.Getpid()) + `,"status":"idle"}`
	if err := os.WriteFile(filepath.Join(sessionsDir, "bridge.json"), []byte(bridge), 0600); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Dir(NewClaude("", home).resolveTranscript(&surface.Session{Cwd: cwd}, "local"))
	if err := os.MkdirAll(transcriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(transcriptDir) })
	if err := os.WriteFile(filepath.Join(transcriptDir, "local.jsonl"), []byte("{\"type\":\"user\",\"uuid\":\"running\",\"message\":{\"content\":\"work\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := NewClaude("", home).List(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].Status != surface.StatusBusy {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestClaudeObserveFindsTurnBeforeToolHeavyTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"user","uuid":"human","message":{"content":"do the work"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4097; index++ {
		if _, err := fmt.Fprintf(file, `{"type":"user","uuid":"tool-%d","message":{"tool_use_id":"tool","content":"result"}}`+"\n", index); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Status: surface.StatusIdle, Transcript: path}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "human" {
		t.Fatalf("inflight observation=%+v err=%v", observation, err)
	}
	appendTestTranscript(t, path, `{"type":"assistant","uuid":"assistant","timestamp":"2026-08-06T01:00:00Z","message":{"id":"done","stop_reason":"end_turn","content":"complete"}}`)
	observation, err = claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusIdle || observation.CompletedTurnID != "done" || observation.Reply == nil || observation.Reply.UserText != "do the work" {
		t.Fatalf("completed observation=%+v err=%v", observation, err)
	}
}

func TestClaudeObserveResetsAfterSameSizeReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	old := `{"type":"user","uuid":"old","message":{"content":"old"}}` + "\n"
	new := `{"type":"user","uuid":"new","message":{"content":"new"}}` + "\n"
	if len(old) != len(new) {
		t.Fatalf("replacement lengths differ")
	}
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Status: surface.StatusIdle, Transcript: path}
	if _, err := claude.Observe(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	if err := os.WriteFile(replacement, []byte(new), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "new" || observation.CompletedTurnID != "" {
		t.Fatalf("replacement observation=%+v err=%v", observation, err)
	}
}

func TestClaudeObserveRejectsOversizedAppendedRecord(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	claude := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}
	if _, err := claude.Observe(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	appendTestTranscript(t, path, strings.Repeat("x", maxClaudeTranscriptRecordBytes+1))
	if _, err := claude.Observe(context.Background(), session); err == nil {
		t.Fatal("oversized appended record was buffered instead of rejected")
	}
}

func TestClaudeObserveAndModelUseCompletedTranscript(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"model-a","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"user","uuid":"u2","message":{"content":"running"}}`)
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Status: surface.StatusBusy, Transcript: path}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ActiveTurnID != "u2" || observation.CompletedTurnID != "m1" || observation.InputTurnID != "u1" || observation.Reply.Text != "answer" {
		t.Fatalf("observation=%+v", observation)
	}
	model, err := claude.Model(context.Background(), session, "")
	if err != nil || model != "model-a" {
		t.Fatalf("model=%q err=%v", model, err)
	}
}

func TestClaudeInterruptedTurnIsNotReportedBusyOrComplete(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"model-a","stop_reason":"stop_sequence","content":[{"type":"text","text":"partial"}]}}`)
	claude := NewClaude("Default", t.TempDir())
	observation, err := claude.Observe(context.Background(), &surface.Session{ID: "bridge", Transcript: path})
	if err != nil || observation.Status == surface.StatusBusy || observation.CompletedTurnID != "" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if err := claude.Stream(context.Background(), &surface.Session{ID: "bridge", Transcript: path}, "u1", func(surface.StreamEvent) {}, time.Second); err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("stream err=%v", err)
	}
}

func TestClaudeStreamTreatsUserInterruptMarkerAsTargetedFailure(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":null,"content":[{"type":"text","text":"partial"}]}}
{"type":"user","uuid":"interrupt","message":{"content":"[Request interrupted by user]"}}`)
	claude := NewClaude("Default", t.TempDir())
	err := claude.Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "u1", func(surface.StreamEvent) {}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("stream err=%v", err)
	}
}

func TestClaudeStreamDoesNotDuplicateTurnDurationCompletion(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	session := seededClaudeStreamSession(t, path, "")
	go func() {
		time.Sleep(50 * time.Millisecond)
		file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		defer file.Close()
		file.WriteString(`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"turn_duration","content":"done"}
{"type":"user","uuid":"u2","message":{"content":"two"}}
{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"second answer"}]}}
{"type":"system","subtype":"turn_duration","content":"done"}
`)
	}()
	claude := NewClaude("Default", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := map[string]int{}
	err := claude.Stream(ctx, session, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done[event.TurnID]++
			if len(done) == 2 {
				cancel()
			}
		}
	}, time.Second)
	if err != context.Canceled || done["u1"] != 1 || done["u2"] != 1 {
		t.Fatalf("stream err=%v done=%v", err, done)
	}
}

func seededClaudeStreamSession(t *testing.T, path, transport string) *surface.Session {
	t.Helper()
	seed, err := readTimeline(context.Background(), path, "claude", 0, 20)
	if err != nil || !seed.TranscriptOffsetSet || seed.TranscriptIdentity == "" {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
	return &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transport: transport, Transcript: path, HasLocal: true, TranscriptOffset: seed.TranscriptOffset, TranscriptOffsetSet: true, TranscriptIdentity: seed.TranscriptIdentity}
}

func TestClaudeSourceStreamResumesAtSeedBoundaryWithoutReplayingHistory(t *testing.T) {
	var history strings.Builder
	for turn := 1; turn <= 30; turn++ {
		fmt.Fprintf(&history, `{"type":"user","uuid":"u%d","message":{"content":"question number %d"}}`+"\n", turn, turn)
		fmt.Fprintf(&history, `{"type":"assistant","uuid":"a%d","message":{"id":"m%d","stop_reason":"end_turn","content":[{"type":"text","text":"answer number %d"}]}}`+"\n", turn, turn, turn)
	}
	path := writeTranscript(t, history.String())
	session := seededClaudeStreamSession(t, path, "")
	appended := `{"type":"user","uuid":"between","message":{"content":"written between seed and tail"}}` + "\n"
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(appended); err != nil {
		t.Fatal(err)
	}
	file.Close()
	claude := NewClaude("Default", t.TempDir())
	if _, err := claude.Observe(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	collect := func() []surface.StreamEvent {
		var events []surface.StreamEvent
		err := claude.Stream(context.Background(), session, "", func(event surface.StreamEvent) { events = append(events, event) }, 400*time.Millisecond)
		if !errors.Is(err, surface.ErrStreamWindow) {
			t.Fatalf("stream err=%v", err)
		}
		return events
	}
	events := collect()
	if len(events) != 1 || events[0].Text != "written between seed and tail" {
		t.Fatalf("first window replayed history or missed the handoff gap: %+v", events)
	}
	if again := collect(); len(again) != 0 {
		t.Fatalf("restarted window re-emitted events: %+v", again)
	}
}

func TestClaudeObserveDerivesTurnStateFromTranscript(t *testing.T) {
	const completed = `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"model-a","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}`
	live := os.Getpid()
	notBusy := func(t *testing.T, observation *surface.TurnObservation) {
		t.Helper()
		if observation.Status == surface.StatusBusy || observation.ActiveTurnID != "" {
			t.Fatalf("observation=%+v", observation)
		}
	}
	for _, test := range []struct {
		name       string
		transcript string
		session    surface.Session
		check      func(*testing.T, *surface.TurnObservation)
	}{
		{
			name: "completed turn clears previously busy status", transcript: completed,
			session: surface.Session{Status: surface.StatusBusy},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.ActiveTurnID != "" || o.CompletedTurnID != "m1" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "completion overrides unknown remote status", transcript: completed,
			session: surface.Session{Status: surface.StatusUnknown},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "completion overrides stale offline status for live process", transcript: completed,
			session: surface.Session{PID: live, Status: surface.StatusOffline},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "completed turn does not revive offline session", transcript: completed,
			session: surface.Session{Status: surface.StatusOffline},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusOffline || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "empty transcript keeps busy", transcript: "",
			session: surface.Session{PID: live, Status: surface.StatusBusy},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusBusy || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "empty transcript does not prove idle", transcript: "",
			session: surface.Session{PID: live, Status: surface.StatusIdle},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusUnknown || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "max_tokens releases turn without completion",
			transcript: `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}}`,
			session: surface.Session{Status: surface.StatusBusy},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.ActiveTurnID != "" || o.CompletedTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "context window exhaustion releases turn without completion",
			transcript: `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"model_context_window_exceeded","content":[{"type":"text","text":"partial"}]}}`,
			session: surface.Session{Status: surface.StatusBusy},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.ActiveTurnID != "" || o.CompletedTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "turn duration closes tool-ending turn",
			transcript: `
{"type":"user","uuid":"u1","message":{"content":"work"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"text","text":"done"}]}}
{"type":"user","uuid":"tool-result","message":{"content":[{"type":"tool_result","tool_use_id":"tool1","content":"ok"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-07-19T01:29:40.594Z"}`,
			session: surface.Session{Status: surface.StatusBusy},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusIdle || o.CompletedTurnID != "m1" || o.Reply == nil || o.Reply.Text != "done" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "completion older than bridge activity does not clear a new busy turn",
			transcript: `
{"type":"user","uuid":"u1","timestamp":"2026-07-19T01:00:00Z","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-07-19T01:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}`,
			session: surface.Session{Status: surface.StatusBusy, LastActive: time.Date(2026, 7, 19, 1, 5, 0, 0, time.UTC)},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusBusy {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "user interrupt marker terminates partial turn",
			transcript: `
{"type":"user","uuid":"u1","message":{"content":"long answer"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":null,"content":[{"type":"text","text":"partial"}]}}
{"type":"user","uuid":"interrupt","message":{"content":"[Request interrupted by user]"}}`,
			check: func(t *testing.T, o *surface.TurnObservation) {
				notBusy(t, o)
				if o.CompletedTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "slash commands are not active turns",
			transcript: completed + `
{"type":"user","uuid":"command","message":{"content":"/compact"}}
{"type":"user","uuid":"metadata","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
{"type":"system","subtype":"local_command","content":"<local-command-stdout>Compacted</local-command-stdout>"}`,
			check: func(t *testing.T, o *surface.TurnObservation) {
				notBusy(t, o)
				if o.CompletedTurnID != "m1" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "compaction summary is not an active turn",
			transcript: completed + `
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}
{"type":"user","uuid":"summary","message":{"content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."}}`,
			check: func(t *testing.T, o *surface.TurnObservation) {
				notBusy(t, o)
				if o.CompletedTurnID != "m1" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name:       "pending compact does not revive offline session",
			transcript: `{"type":"user","uuid":"compact","message":{"content":"/compact"}}`,
			session:    surface.Session{Status: surface.StatusOffline},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusOffline || o.ActiveTurnID != "" {
					t.Fatalf("observation=%+v", o)
				}
			},
		},
		{
			name: "compact boundary does not consume a following compact",
			transcript: `
{"type":"user","uuid":"c1","message":{"content":"/compact"}}
{"type":"user","uuid":"c2","message":{"content":"/compact"}}
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}
{"type":"user","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}
{"type":"user","message":{"content":"<local-command-stdout>Compacted</local-command-stdout>"}}`,
			session: surface.Session{Status: surface.StatusIdle},
			check: func(t *testing.T, o *surface.TurnObservation) {
				if o.Status != surface.StatusBusy {
					t.Fatalf("second compact was not pending: observation=%+v", o)
				}
			},
		},
		{
			name: "compact completion uses timestamps across reordered records",
			transcript: `
{"type":"user","timestamp":"2026-07-18T08:09:58.695Z","message":{"content":"/compact"}}
{"type":"system","subtype":"compact_boundary","timestamp":"2026-07-18T08:12:08.605Z","content":"Conversation compacted"}
{"type":"user","timestamp":"2026-07-18T08:12:08.359Z","message":{"content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."}}
{"type":"user","timestamp":"2026-07-18T08:09:58.696Z","message":{"content":"<command-name>/compact</command-name><command-args></command-args>"}}`,
			session: surface.Session{Status: surface.StatusIdle},
			check:   notBusy,
		},
		{
			name: "compact followed by a completed turn is not pending",
			transcript: `
{"type":"user","timestamp":"2026-07-19T07:30:52.908Z","message":{"content":"/compact"}}
{"type":"assistant","timestamp":"2026-07-19T07:43:04.867Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-07-19T07:43:04.906Z"}`,
			session: surface.Session{Status: surface.StatusIdle},
			check:   notBusy,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := test.session
			session.ID = "bridge"
			session.Transcript = writeTranscript(t, test.transcript)
			observation, err := NewClaude("Default", t.TempDir()).Observe(context.Background(), &session)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, observation)
		})
	}
}

func TestClaudeCompactRemainsBusyUntilCommandCompletes(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"user","uuid":"compact","message":{"content":"/compact"}}`)
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "bridge", Status: surface.StatusIdle, Transcript: path}

	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "compact" {
		t.Fatalf("pending observation=%+v err=%v", observation, err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{\"type\":\"user\",\"message\":{\"content\":\"<command-name>/compact</command-name><command-args></command-args>\"}}\n{\"type\":\"system\",\"subtype\":\"local_command\",\"content\":\"<local-command-stdout>Not enough messages to compact.</local-command-stdout>\"}\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	observation, err = claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
		t.Fatalf("completed observation=%+v err=%v", observation, err)
	}
}

func TestClaudeStreamUsesSessionTranscriptAndStandaloneActiveTurn(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}
	go func() {
		time.Sleep(50 * time.Millisecond)
		file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		defer file.Close()
		file.WriteString("{\"type\":\"assistant\",\"uuid\":\"a1\",\"message\":{\"id\":\"m1\",\"stop_reason\":\"end_turn\",\"content\":[{\"type\":\"text\",\"text\":\"answer\"}]}}\n")
	}()
	var events []surface.StreamEvent
	err := claude.Stream(context.Background(), session, "u1", func(event surface.StreamEvent) { events = append(events, event) }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var user, assistant, done *surface.StreamEvent
	for index := range events {
		event := &events[index]
		switch {
		case event.Role == "user":
			user = event
		case event.Role == "assistant" && event.Kind == "message":
			assistant = event
		case event.Kind == "done":
			done = event
		}
	}
	if user == nil || assistant == nil || done == nil || assistant.Text != "answer" || assistant.TurnID != "u1" || done.TurnID != "u1" {
		t.Fatalf("events=%+v", events)
	}
	if assistant.ProviderKey == "" || !strings.HasPrefix(assistant.ProviderKey, "timeline:") || assistant.CallID != "" {
		t.Fatalf("assistant identity=%+v", assistant)
	}
}

func TestClaudeStreamSupportsUDSTranscriptTail(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	claude := NewClaude("Default", t.TempDir())
	session := seededClaudeStreamSession(t, path, "uds")
	go func() {
		time.Sleep(50 * time.Millisecond)
		file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		defer file.Close()
		file.WriteString(`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}` + "\n")
	}()
	var events []surface.StreamEvent
	streamContext, cancel := context.WithCancel(context.Background())
	err := claude.Stream(streamContext, session, "", func(event surface.StreamEvent) {
		events = append(events, event)
		if event.Kind == "done" {
			cancel()
		}
	}, time.Second)
	if err != context.Canceled {
		t.Fatalf("stream err=%v", err)
	}
	if len(events) != 2 || events[0].Kind != "message" || events[0].Text != "answer" || events[0].TurnID != "u1" || events[1].Kind != "done" {
		t.Fatalf("events=%+v", events)
	}
	page, err := readTimeline(context.Background(), path, "claude", 0, 20)
	if err != nil || len(page.Items) != 2 || page.Items[1].ID != events[0].ID {
		t.Fatalf("page/live identity mismatch: page=%+v live=%q err=%v", page, events[0].ID, err)
	}
}

func TestClaudeUDSStreamRejectsUUIDSpecificCorrelation(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"one"}}`)
	claude := NewClaude("Default", t.TempDir())
	err := claude.Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transport: "uds", Transcript: path}, "u1", func(surface.StreamEvent) {}, time.Second)
	if err != surface.ErrUnsupported {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeUDSStreamPreservesFutureToolCallAndResultCorrelation(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"inspect"}}`)
	claude := NewClaude("Default", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		defer file.Close()
		file.WriteString(`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":null,"content":[{"type":"tool_use","id":"call-1","name":"Read","input":{"path":"x"}}]}}` + "\n")
		file.WriteString(`{"type":"user","uuid":"u-tool","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":"contents"}]}}` + "\n")
		file.WriteString(`{"type":"assistant","uuid":"a2","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}` + "\n")
	}()
	var events []surface.StreamEvent
	err := claude.Stream(ctx, &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transport: "uds", Transcript: path}, "", func(event surface.StreamEvent) {
		events = append(events, event)
		if event.Kind == "done" {
			cancel()
		}
	}, time.Second)
	if err != context.Canceled {
		t.Fatalf("err=%v events=%+v", err, events)
	}
	var call, result bool
	for _, event := range events {
		if event.Kind == "toolCall" && event.CallID == "call-1" && event.Title == "Read" {
			call = true
		}
		if event.Kind == "toolResult" && event.CallID == "call-1" {
			result = true
		}
	}
	if !call || !result {
		t.Fatalf("events=%+v", events)
	}
}

func TestClaudeStreamWaitsForNewTurnAfterCompletedBaseline(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}`)
	claude := NewClaude("Default", t.TempDir())
	var events []surface.StreamEvent
	err := claude.Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "u2", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 350*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("stream err=%v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events=%+v", events)
	}
}

func TestClaudeStreamKeepsTargetTurnAcrossLongTranscriptOverlap(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"target","message":{"content":"inspect"}}`)
	claude := NewClaude("Default", t.TempDir())
	if _, err := claude.Observe(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	padding := `{"type":"system","subtype":"background"}` + "\n"
	for written := 0; written < initialClaudeObservationBytes+(1<<20); written += len(padding) {
		if _, err := file.WriteString(padding); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if _, err := file.WriteString(`{"type":"assistant","uuid":"assistant-target","message":{"id":"target-answer","stop_reason":"end_turn","content":[{"type":"text","text":"target answer"}]}}` + "\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var events []surface.StreamEvent
	err = claude.Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "target", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var sawAnswer, sawDone bool
	for _, event := range events {
		if event.TurnID != "target" {
			t.Fatalf("event escaped target turn: %+v", event)
		}
		if event.Kind == "message" && event.Text == "target answer" {
			sawAnswer = true
		}
		if event.Kind == "done" {
			sawDone = true
		}
	}
	if !sawAnswer || !sawDone {
		t.Fatalf("events=%+v", events)
	}
}

func TestClaudeStreamDoesNotCreditOlderTurnCompletionInOverlapToNewestTurn(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"older","message":{"content":"first"}}`)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	padding := `{"type":"system","subtype":"background"}` + "\n"
	for written := 0; written < initialClaudeObservationBytes+(1<<20); written += len(padding) {
		if _, err := file.WriteString(padding); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	for _, record := range []string{
		`{"type":"assistant","uuid":"assistant-older","message":{"id":"older-answer","stop_reason":"end_turn","content":[{"type":"text","text":"older answer"}]}}`,
		`{"type":"system","subtype":"turn_duration","durationMs":10}`,
		`{"type":"user","uuid":"newest","message":{"content":"second"}}`,
	} {
		if _, err := file.WriteString(record + "\n"); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("Default", t.TempDir())
	var events []surface.StreamEvent
	err = claude.Stream(context.Background(), &surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: path}, "newest", func(event surface.StreamEvent) {
		events = append(events, event)
	}, time.Second)
	if !errors.Is(err, surface.ErrStreamWindow) {
		t.Fatalf("stream err=%v events=%+v", err, events)
	}
	for _, event := range events {
		if event.Kind == "done" || event.Text == "older answer" {
			t.Fatalf("older turn credited to newest: %+v", event)
		}
	}
}

func TestClaudeTranscriptPreservesMultiMegabyteUnicodeReply(t *testing.T) {
	reply := strings.Repeat("界", 2*1024*1024)
	record, err := json.Marshal(map[string]any{"type": "assistant", "uuid": "a", "message": map[string]any{"id": "m", "model": "model-a", "stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": reply}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, `{"type":"user","uuid":"u","message":{"content":"long"}}`+"\n"+string(record))
	observation, err := NewClaude("Default", t.TempDir()).Observe(context.Background(), &surface.Session{ID: "bridge", Transcript: path})
	if err != nil || observation.CompletedTurnID != "m" || observation.Reply == nil {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if observation.Reply.Text != reply {
		t.Fatalf("reply_bytes=%d want=%d", len(observation.Reply.Text), len(reply))
	}
}
