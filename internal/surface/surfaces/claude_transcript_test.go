package surfaces

import (
	"context"
	"encoding/json"
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

func TestReadClaudeTurnsRequiresEndTurnAndKeepsTurnIdentity(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"model-a","stop_reason":null,"content":[{"type":"text","text":"partial"}]}}
{"type":"assistant","uuid":"a2","message":{"id":"m1","model":"model-a","stop_reason":"end_turn","content":[{"type":"text","text":"same"}]}}
{"malformed":
{"type":"user","uuid":"u2","message":{"content":"two"}}
{"type":"assistant","uuid":"a3","message":{"id":"m2","model":"model-a","stop_reason":"end_turn","content":[{"type":"text","text":"same"}]}}`)
	turns, err := readClaudeTurns(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns=%+v", turns)
	}
	if !turns[0].Done || turns[0].MessageID != "m1" || !strings.Contains(turns[0].Assistant, "same") {
		t.Fatalf("first=%+v", turns[0])
	}
	if !turns[1].Done || turns[1].MessageID != "m2" || turns[1].Assistant != "same" {
		t.Fatalf("second=%+v", turns[1])
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
	if observation.ActiveTurnID != "u2" || observation.CompletedTurnID != "m1" || observation.Reply.Text != "answer" {
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

func TestClaudeUserInterruptMarkerTerminatesPartialTurn(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"long answer"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":null,"content":[{"type":"text","text":"partial"}]}}
{"type":"user","uuid":"interrupt","message":{"content":"[Request interrupted by user]"}}`)
	claude := NewClaude("Default", t.TempDir())
	observation, err := claude.Observe(context.Background(), &surface.Session{ID: "bridge", Transcript: path})
	if err != nil || observation.Status == surface.StatusBusy || observation.ActiveTurnID != "" || observation.CompletedTurnID != "" {
		t.Fatalf("observation=%+v err=%v", observation, err)
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
	err := claude.Stream(context.Background(), session, "", func(event surface.StreamEvent) { events = append(events, event) }, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Kind != "text" || events[0].Text != "answer" || events[1].Kind != "done" {
		t.Fatalf("events=%+v", events)
	}
}

func TestClaudeTranscriptPreservesMultiMegabyteUnicodeReply(t *testing.T) {
	reply := strings.Repeat("界", 2*1024*1024)
	record, err := json.Marshal(map[string]any{"type": "assistant", "uuid": "a", "message": map[string]any{"id": "m", "model": "model-a", "stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": reply}}}})
	if err != nil {
		t.Fatal(err)
	}
	path := writeTranscript(t, `{"type":"user","uuid":"u","message":{"content":"long"}}`+"\n"+string(record))
	turns, err := readClaudeTurns(path)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns=%d err=%v", len(turns), err)
	}
	if turns[0].Assistant != reply {
		t.Fatalf("reply_bytes=%d want=%d", len(turns[0].Assistant), len(reply))
	}
}

func TestClaudeCommandResultStripsMarkupAndANSI(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
{"type":"system","subtype":"local_command","content":"<local-command-stdout>Compacted</local-command-stdout>"}
{"type":"user","message":{"content":"<command-name>/model</command-name>\n<command-args>bad</command-args>"}}
{"type":"system","subtype":"local_command","content":"<local-command-stdout>Model 'bad' not found\u001b[2m</local-command-stdout>"}`)
	result, found, err := readClaudeCommandResult(path, 0, "/model", "bad")
	if err != nil || !found || result != "Model 'bad' not found" {
		t.Fatalf("result=%q found=%v err=%v", result, found, err)
	}
}

func TestClaudeSlashCommandsAreNotActiveTurns(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"user","uuid":"command","message":{"content":"/compact"}}
{"type":"user","uuid":"metadata","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
{"type":"system","subtype":"local_command","content":"<local-command-stdout>Compacted</local-command-stdout>"}`)
	claude := NewClaude("Default", t.TempDir())
	observation, err := claude.Observe(context.Background(), &surface.Session{ID: "bridge", Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status == surface.StatusBusy || observation.ActiveTurnID != "" || observation.CompletedTurnID != "m1" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestClaudeCompactionSummaryIsNotAnActiveTurn(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"u1","message":{"content":"one"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}}
{"type":"system","subtype":"compact_boundary","content":"Conversation compacted"}
{"type":"user","uuid":"summary","message":{"content":"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion."}}`)
	claude := NewClaude("Default", t.TempDir())
	observation, err := claude.Observe(context.Background(), &surface.Session{ID: "bridge", Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status == surface.StatusBusy || observation.ActiveTurnID != "" || observation.CompletedTurnID != "m1" {
		t.Fatalf("observation=%+v", observation)
	}
}
