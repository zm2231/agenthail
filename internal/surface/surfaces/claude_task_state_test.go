package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func taskIDs(tasks []surface.BackgroundTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	slices.Sort(ids)
	return ids
}

func launch(t *testing.T, at time.Time, call, task string) string {
	t.Helper()
	return mustJSON(t, backgroundCall(at, call, "Bash", map[string]any{"command": "sleep 60", "run_in_background": true})) + "\n" +
		mustJSON(t, backgroundResult(at, call, map[string]any{"backgroundTaskId": task})) + "\n"
}

func fullScan(t *testing.T, path string) []string {
	t.Helper()
	return taskIDs(NewClaude(nil, t.TempDir()).transcriptTasks(context.Background(), path))
}

func readSavedScan(t *testing.T, c *Claude, path string) ([]byte, claudeTaskScan) {
	t.Helper()
	raw, err := os.ReadFile(c.taskStatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	var scan claudeTaskScan
	if err := json.Unmarshal(raw, &scan); err != nil {
		t.Fatal(err)
	}
	return raw, scan
}

func writeSavedScan(t *testing.T, c *Claude, path string, scan claudeTaskScan) {
	t.Helper()
	raw, err := json.Marshal(scan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.taskStatePath(path), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAProcessResumesATranscriptsTasksFromTheScanAnotherSaved(t *testing.T) {
	home, now := t.TempDir(), time.Now().UTC()
	path := writeTranscript(t, launch(t, now, "call-1", "b-1"))
	daemon := NewClaude(nil, home)
	if got := taskIDs(daemon.transcriptTasks(context.Background(), path)); !slices.Equal(got, []string{"b-1"}) {
		t.Fatalf("tasks = %v", got)
	}
	_, saved := readSavedScan(t, daemon, path)
	if saved.Offset != fileSize(t, path) || saved.Path != path {
		t.Fatalf("saved = %+v", saved)
	}
	saved.Running = append(saved.Running, surface.BackgroundTask{ID: "b-saved", Kind: surface.BackgroundShell})
	writeSavedScan(t, daemon, path, saved)
	command := NewClaude(nil, home)
	if got := taskIDs(command.transcriptTasks(context.Background(), path)); !slices.Equal(got, []string{"b-1", "b-saved"}) {
		t.Fatalf("a later process reads on from the saved offset: %v", got)
	}
	appendTranscript(t, path, mustJSON(t, backgroundNotification(now.Add(time.Second), "b-1"))+"\n")
	if got := taskIDs(command.transcriptTasks(context.Background(), path)); !slices.Equal(got, []string{"b-saved"}) {
		t.Fatalf("tasks = %v", got)
	}
}

func TestAnOlderSavedScanOnlyCostsARereadOfWhatFollowsIt(t *testing.T) {
	home, now := t.TempDir(), time.Now().UTC()
	path := writeTranscript(t, launch(t, now, "call-1", "b-1"))
	first := NewClaude(nil, home)
	first.transcriptTasks(context.Background(), path)
	older, _ := readSavedScan(t, first, path)
	appendTranscript(t, path, launch(t, now.Add(time.Second), "call-2", "b-2")+mustJSON(t, backgroundNotification(now.Add(2*time.Second), "b-1"))+"\n")
	second := NewClaude(nil, home)
	second.transcriptTasks(context.Background(), path)
	if _, saved := readSavedScan(t, second, path); saved.Offset != fileSize(t, path) {
		t.Fatalf("saved offset = %d", saved.Offset)
	}
	if err := os.WriteFile(first.taskStatePath(path), older, 0o600); err != nil {
		t.Fatal(err)
	}
	got := taskIDs(NewClaude(nil, home).transcriptTasks(context.Background(), path))
	if want := fullScan(t, path); !slices.Equal(got, want) || !slices.Equal(got, []string{"b-2"}) {
		t.Fatalf("tasks = %v, full scan = %v", got, want)
	}
}

func TestASavedScanThatNoLongerMatchesItsTranscriptIsReadFromTheStart(t *testing.T) {
	now := time.Now().UTC()
	for name, change := range map[string]func(t *testing.T, path string){
		"rewritten": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(launch(t, now, "call-9", "b-9")+launch(t, now, "call-8", "b-8")), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"truncated": func(t *testing.T, path string) {
			if err := os.Truncate(path, fileSize(t, path)/2); err != nil {
				t.Fatal(err)
			}
			appendTranscript(t, path, "\n"+launch(t, now, "call-9", "b-9"))
		},
		"shorter than the offset": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(launch(t, now, "call-9", "b-9")), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			path := writeTranscript(t, launch(t, now, "call-1", "b-1")+launch(t, now, "call-2", "b-2"))
			first := NewClaude(nil, home)
			first.transcriptTasks(context.Background(), path)
			_, saved := readSavedScan(t, first, path)
			saved.Running = append(saved.Running, surface.BackgroundTask{ID: "b-saved", Kind: surface.BackgroundShell})
			writeSavedScan(t, first, path, saved)
			change(t, path)
			got := taskIDs(NewClaude(nil, home).transcriptTasks(context.Background(), path))
			if want := fullScan(t, path); !slices.Equal(got, want) || slices.Contains(got, "b-saved") {
				t.Fatalf("tasks = %v, full scan = %v", got, want)
			}
		})
	}
}

func TestATranscriptRewrittenInPlaceIsReadAgainByTheProcessFollowingIt(t *testing.T) {
	now := time.Now().UTC()
	path := writeTranscript(t, launch(t, now, "call-1", "b-1"))
	c := NewClaude(nil, t.TempDir())
	c.transcriptTasks(context.Background(), path)
	if err := os.WriteFile(path, []byte(launch(t, now, "call-2", "b-2")), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := taskIDs(c.transcriptTasks(context.Background(), path)); !slices.Equal(got, []string{"b-2"}) {
		t.Fatalf("tasks = %v", got)
	}
}

func TestATaskStartedBeforeARecordStampedEarlierStillRuns(t *testing.T) {
	started := time.Now().UTC().Add(-time.Second)
	copied := mustJSON(t, map[string]any{"type": "user", "timestamp": started.Add(-time.Hour).Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "kept by a compaction"}})
	c, sess, _ := terminalBackground(t, "shell")
	sess.Transcript = writeTranscript(t, launch(t, started.Add(2*time.Second), "call-new", "b-new")+copied+"\n")
	background := c.claudeBackground(context.Background(), sess)
	if background == nil || len(background.Tasks) != 1 || background.Tasks[0].ID != "b-new" {
		t.Fatalf("background = %+v", background)
	}
}

func TestAFailedCallsErrorResultEndsItsPendingCall(t *testing.T) {
	now := time.Now().UTC()
	failed := map[string]any{"type": "user", "timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-fail", "content": "Error: exit 1", "is_error": true}}}, "toolUseResult": "Error: exit 1"}
	path := writeTranscript(t, mustJSON(t, backgroundCall(now, "call-fail", "Bash", map[string]any{"command": "false"}))+"\n"+mustJSON(t, failed)+"\n")
	c := NewClaude(nil, t.TempDir())
	c.transcriptTasks(context.Background(), path)
	if calls := c.taskScans[path].Calls; len(calls) != 0 {
		t.Fatalf("pending calls = %v", calls)
	}
}

func TestARequestClaudeRecordsDoesNotReopenACallAlreadyAnswered(t *testing.T) {
	now := time.Now().UTC()
	call := backgroundCall(now, "call-1", "Bash", map[string]any{"command": "ls"})
	request := map[string]any{"type": "api-request-blob", "sessionId": "conversation-test", "hash": "h", "message": call["message"]}
	path := writeTranscript(t, mustJSON(t, call)+"\n"+mustJSON(t, backgroundResult(now, "call-1", map[string]any{"stdout": "x"}))+"\n"+mustJSON(t, request)+"\n")
	c := NewClaude(nil, t.TempDir())
	c.transcriptTasks(context.Background(), path)
	if calls := c.taskScans[path].Calls; len(calls) != 0 {
		t.Fatalf("pending calls = %v", calls)
	}
}

func TestARecordTooLargeToReadDoesNotHideTheTasksAfterIt(t *testing.T) {
	now := time.Now().UTC()
	huge := mustJSON(t, map[string]any{"type": "user", "timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{"content": strings.Repeat("x", maxClaudeTranscriptRecordBytes)}})
	path := writeTranscript(t, huge+"\n"+launch(t, now, "call-new", "b-new"))
	c := NewClaude(nil, t.TempDir())
	if got := taskIDs(c.transcriptTasks(context.Background(), path)); !slices.Equal(got, []string{"b-new"}) {
		t.Fatalf("tasks = %v", got)
	}
	if offset := c.taskScans[path].Offset; offset != fileSize(t, path) {
		t.Fatalf("offset = %d, size = %d", offset, fileSize(t, path))
	}
}

func TestSavedScansOfTranscriptsThatAreGoneAreRemoved(t *testing.T) {
	home, now := t.TempDir(), time.Now().UTC()
	gone := writeTranscript(t, launch(t, now, "call-1", "b-1"))
	kept := writeTranscript(t, launch(t, now, "call-2", "b-2"))
	NewClaude(nil, home).transcriptTasks(context.Background(), gone)
	NewClaude(nil, home).transcriptTasks(context.Background(), kept)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, kept, launch(t, now, "call-3", "b-3"))
	c := NewClaude(nil, home)
	c.transcriptTasks(context.Background(), kept)
	if _, err := os.Stat(c.taskStatePath(gone)); !os.IsNotExist(err) {
		t.Fatalf("scan of a removed transcript: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(c.taskStatePath(kept)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, %v", entries, err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
