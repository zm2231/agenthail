package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// writeClaudeRecord writes the process record Claude keeps for this test's
// own live pid, with fields as Claude 2.1.303 writes them.
func writeClaudeRecord(t *testing.T, home string, fields map[string]any) {
	t.Helper()
	record := map[string]any{"pid": os.Getpid(), "sessionId": "conversation", "bridgeSessionId": "bridge", "cwd": "/fixture"}
	for key, value := range fields {
		record[key] = value
	}
	data, _ := json.Marshal(record)
	path := filepath.Join(home, ".claude", "sessions", fmt.Sprint(os.Getpid())+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeObserveReadsWaitingFromTheProcessRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	transcript := writeTranscript(t, `{"type":"user","uuid":"prompt","timestamp":"2026-10-18T23:00:27Z","message":{"content":"Ask me which color I prefer"}}`)
	asked := time.Date(2026, 10, 18, 23, 0, 32, 213000000, time.UTC)
	for _, test := range []struct {
		name   string
		fields map[string]any
		want   *surface.Waiting
	}{
		{
			name:   "AskUserQuestion",
			fields: map[string]any{"status": "waiting", "waitingFor": "input needed", "statusUpdatedAt": asked.UnixMilli()},
			want:   &surface.Waiting{Kind: surface.WaitingQuestion, ID: fmt.Sprintf("conversation:question:%d", asked.UnixMilli()), Since: asked},
		},
		{
			name:   "plan approval or tool permission",
			fields: map[string]any{"status": "waiting", "waitingFor": "permission prompt", "statusUpdatedAt": asked.UnixMilli()},
			want:   &surface.Waiting{Kind: surface.WaitingApproval, ID: fmt.Sprintf("conversation:approval:%d", asked.UnixMilli()), Since: asked},
		},
		{name: "working", fields: map[string]any{"status": "busy", "statusUpdatedAt": asked.UnixMilli()}},
		{name: "answered and finished", fields: map[string]any{"status": "idle", "statusUpdatedAt": asked.Add(time.Minute).UnixMilli()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeClaudeRecord(t, home, test.fields)
			claude := NewClaude(nil, home)
			session := &surface.Session{ID: "conversation", Bridge: "bridge", Surface: surface.KindClaude, PID: os.Getpid(), Status: surface.StatusBusy, Transcript: transcript}
			observation, err := claude.Observe(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if observation.WaitingOn != nil {
					t.Fatalf("waiting=%+v", observation.WaitingOn)
				}
				return
			}
			if observation.Status != surface.StatusBusy || observation.WaitingOn == nil || *observation.WaitingOn != *test.want {
				t.Fatalf("observation=%+v waiting=%+v", observation, observation.WaitingOn)
			}
		})
	}
}

func TestClaudeObserveIgnoresARecordOfAnotherSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeClaudeRecord(t, home, map[string]any{"sessionId": "other", "bridgeSessionId": "bridge", "status": "waiting", "waitingFor": "input needed", "statusUpdatedAt": 1})
	claude := NewClaude(nil, home)
	session := &surface.Session{ID: "conversation", Bridge: "bridge", Surface: surface.KindClaude, PID: os.Getpid(), Status: surface.StatusBusy, Transcript: writeTranscript(t, `{"type":"user","uuid":"prompt","message":{"content":"Plan it"}}`)}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.WaitingOn != nil {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}
