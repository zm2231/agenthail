package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func writeClaudeJob(t *testing.T, home, jobID, sessionID, resumeID string, updated time.Time) {
	t.Helper()
	path := filepath.Join(home, ".claude", "jobs", jobID, "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"template": "bg", "state": "failed", "sessionId": sessionID, "resumeSessionId": resumeID, "createdAt": updated.Add(-time.Minute).UTC().Format(time.RFC3339Nano), "updatedAt": updated.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func jobIDs(runs []surface.ClaudeRunObservation) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.JobID)
	}
	slices.Sort(ids)
	return ids
}

func TestALiveSessionShowsOnlyTheRunsOfItsRunningProcess(t *testing.T) {
	process := exec.Command("sleep", "30")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	started, err := processStartedAt(process.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	recordAt := started.Add(time.Second)
	writePeerRecord(t, home, process.Process.Pid, map[string]any{"pid": float64(process.Process.Pid), "sessionId": "keystone", "startedAt": float64(recordAt.UnixMilli())})
	writeClaudeJob(t, home, "july", "keystone", "", time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	writeClaudeJob(t, home, "gap", "keystone", "", recordAt.Add(-500*time.Millisecond))
	writeClaudeJob(t, home, "now-a", "keystone", "", recordAt.Add(time.Minute))
	writeClaudeJob(t, home, "now-b", "", "keystone", recordAt.Add(2*time.Minute))
	writeClaudeJob(t, home, "elsewhere", "other", "", recordAt.Add(time.Minute))
	claude := NewClaude(nil, home)

	live, err := claude.ObserveSessionClaudeRuns(context.Background(), &surface.Session{ID: "keystone", Surface: surface.KindClaude, PID: process.Process.Pid})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDs(live); !slices.Equal(got, []string{"now-a", "now-b"}) {
		t.Fatalf("a running session shows %v, want every run since its record's startedAt, not the July one or one between the process start and the record", got)
	}

	ended, err := claude.ObserveSessionClaudeRuns(context.Background(), &surface.Session{ID: "keystone", Surface: surface.KindClaude})
	if err != nil {
		t.Fatal(err)
	}
	if got := jobIDs(ended); !slices.Equal(got, []string{"now-b"}) {
		t.Fatalf("a session with no running process shows %v, want only its latest run", got)
	}

	none, err := claude.ObserveSessionClaudeRuns(context.Background(), &surface.Session{ID: "never-ran", Surface: surface.KindClaude})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("a session with no runs: %+v err=%v, want an empty list", none, err)
	}
}

func TestALiveRecordThatNamesNoStartIsDatedByItsProcess(t *testing.T) {
	process := exec.Command("sleep", "30")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	started, err := processStartedAt(process.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	writePeerRecord(t, home, process.Process.Pid, map[string]any{"pid": float64(process.Process.Pid), "sessionId": "keystone"})
	writeClaudeJob(t, home, "july", "keystone", "", time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	writeClaudeJob(t, home, "before", "keystone", "", started.Add(-time.Hour))
	runs, err := NewClaude(nil, home).ObserveSessionClaudeRuns(context.Background(), &surface.Session{ID: "keystone", Surface: surface.KindClaude, PID: process.Process.Pid})
	if err != nil || len(runs) != 0 {
		t.Fatalf("a live session whose record names no start shows %v (err %v), want no run from before its process", jobIDs(runs), err)
	}
	writeClaudeJob(t, home, "now", "keystone", "", started.Add(time.Minute))
	runs, err = NewClaude(nil, home).ObserveSessionClaudeRuns(context.Background(), &surface.Session{ID: "keystone", Surface: surface.KindClaude, PID: process.Process.Pid})
	if err != nil || !slices.Equal(jobIDs(runs), []string{"now"}) {
		t.Fatalf("runs=%v err=%v, want the run of the running process", jobIDs(runs), err)
	}
}
