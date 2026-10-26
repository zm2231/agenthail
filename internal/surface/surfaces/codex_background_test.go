package surfaces

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexBackgroundRollout() []string {
	return []string{
		`{"timestamp":"2026-10-26T10:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-10-26T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-build","input":"text((await tools.exec_command({cmd:\"make build\", yield_time_ms: 1000})).output)"}}`,
		`{"timestamp":"2026-10-26T10:00:02Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-build","output":[{"type":"input_text","text":"Script running with cell ID 3\nWall time 1.0 seconds\nOutput:\ncompiling\n"}]}}`,
		`{"timestamp":"2026-10-26T10:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-10-26T10:01:00Z","type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"exec-build","command":["/bin/zsh","-lc","make build"],"aggregated_output":"ok\n","exit_code":0,"status":"completed"}}}`,
	}
}

func loadedCodex(threadID string) *Codex {
	c := NewCodex("")
	c.noteLoaded(&codexRuntimes{managed: &managedCodexClient{}, managedLoaded: map[string]bool{threadID: true}})
	return c
}

func codexBackgroundAt(t *testing.T, c *Codex, threadID, path string) *surface.BackgroundTasks {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return c.codexBackground(context.Background(), threadID, path, info)
}

func appendLines(t *testing.T, path string, lines []string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TestACommandLeftRunningIsTheThreadsBackgroundWorkUntilItCompletes(t *testing.T) {
	lines := codexBackgroundRollout()
	path := timelineFixture(t, strings.Join(lines[:4], "\n")+"\n")
	c := loadedCodex("thread")
	background := codexBackgroundAt(t, c, "thread", path)
	if !surface.BackgroundRunning(background) || len(background.Tasks) != 1 || background.Source != surface.BackgroundSourceTranscript {
		t.Fatalf("background=%+v", background)
	}
	task := background.Tasks[0]
	if task.ID != "call-build" || task.Kind != surface.BackgroundShell || task.Description != "make build" || !task.Since.Equal(time.Date(2026, 10, 26, 10, 0, 1, 0, time.UTC)) {
		t.Fatalf("task=%+v", task)
	}
	if fresh := codexBackgroundAt(t, loadedCodex("thread"), "thread", path); !surface.SameBackground(fresh, background) {
		t.Fatalf("a fresh read=%+v, incremental=%+v", fresh, background)
	}
	appendLines(t, path, lines[4:])
	if background := codexBackgroundAt(t, c, "thread", path); background != nil {
		t.Fatalf("after completion background=%+v", background)
	}
}

func TestAThreadNoRuntimeHasLoadedRunsNoBackgroundWork(t *testing.T) {
	path := timelineFixture(t, strings.Join(codexBackgroundRollout()[:4], "\n")+"\n")
	if background := codexBackgroundAt(t, NewCodex(""), "thread", path); background != nil {
		t.Fatalf("never loaded background=%+v", background)
	}
	c := loadedCodex("thread")
	c.noteLoaded(&codexRuntimes{managed: &managedCodexClient{}, managedLoaded: map[string]bool{}})
	if background := codexBackgroundAt(t, c, "thread", path); background != nil {
		t.Fatalf("unloaded background=%+v", background)
	}
	c = loadedCodex("thread")
	var source surface.BackgroundSource = c
	if background := source.Background(context.Background(), surface.Session{ID: "thread", Transcript: path}); !surface.BackgroundRunning(background) {
		t.Fatalf("loaded background=%+v", background)
	}
	c.noteLoaded(&codexRuntimes{})
	if background := source.Background(context.Background(), surface.Session{ID: "thread", Transcript: path}); background != nil {
		t.Fatalf("after the app-server went away background=%+v", background)
	}
}

func TestARuntimeWhoseLoadedThreadsCouldNotBeListedKeepsItsLastListing(t *testing.T) {
	c := loadedCodex("thread")
	c.noteLoaded(&codexRuntimes{managed: &managedCodexClient{}})
	if !c.threadLoaded("thread") {
		t.Fatal("a failed listing forgot the loaded thread")
	}
}

func TestACompletionReadWithoutItsTurnStartStillEndsTheBackgroundCommand(t *testing.T) {
	lines := codexBackgroundRollout()
	path := timelineFixture(t, strings.Join(lines[1:3], "\n")+"\n")
	c := loadedCodex("thread")
	if background := codexBackgroundAt(t, c, "thread", path); !surface.BackgroundRunning(background) {
		t.Fatalf("background=%+v", background)
	}
	appendLines(t, path, lines[4:])
	if background := codexBackgroundAt(t, c, "thread", path); background != nil {
		t.Fatalf("background=%+v", background)
	}
}
