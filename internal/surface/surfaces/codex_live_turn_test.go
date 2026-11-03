package surfaces

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func appendCodexFiller(t *testing.T, path string, at time.Time, size int64) {
	t.Helper()
	line := codexRolloutRecord(at, "response_item", `{"type":"message","content":"`+strings.Repeat("x", 2000)+`"}`) + "\n"
	var filler strings.Builder
	for int64(filler.Len()) < size {
		filler.WriteString(line)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(filler.String()); err != nil {
		t.Fatal(err)
	}
}

func TestACodexTurnLongerThanTheTailWindowStaysWorkingWithItsNewestRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	recency := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	started := recency
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(started.Add(time.Second), "response_item", `{"type":"function_call","name":"exec","call_id":"long"}`))
	appendCodexFiller(t, path, started.Add(time.Minute), codexStatusReadLimit+(1<<20))
	newest := time.Now().Add(-5 * time.Second).Truncate(time.Millisecond)
	appendLine(t, path, codexRolloutRecord(newest, "event_msg", `{"type":"token_count"}`))
	codex := &Codex{}

	session := codexWorkingSession(path, surface.StatusBusy)
	session.LastActive = recency
	codex.reconcileLocalStatus(context.Background(), &session)
	if session.Status != surface.StatusBusy || session.Activity == nil || !session.Activity.BusySince.Equal(started) || !session.Activity.LastProgressAt.Equal(newest) || session.Activity.Tool == nil || session.Activity.Tool.Name != "exec" {
		t.Fatalf("status=%s activity=%+v tool=%+v", session.Status, session.Activity, toolOf(session.Activity))
	}
	if session.LastActive.Before(newest) {
		t.Fatalf("last active %s is older than the newest row %s", session.LastActive, newest)
	}

	later := newest.Add(3 * time.Second)
	appendLine(t, path, codexRolloutRecord(later, "event_msg", `{"type":"token_count"}`))
	next := codexWorkingSession(path, surface.StatusBusy)
	next.LastActive = recency
	next, err := codex.LocalStatus(context.Background(), next)
	if err != nil || next.Activity == nil || !next.Activity.LastProgressAt.Equal(later) || next.LastActive.Before(later) {
		t.Fatalf("an appended row did not move the activity: %+v last=%s err=%v", next.Activity, next.LastActive, err)
	}

	unloaded := codexWorkingSession(path, surface.StatusUnknown)
	unloaded.ID = "unloaded"
	(&Codex{}).reconcileLocalStatus(context.Background(), &unloaded)
	if unloaded.Activity != nil || unloaded.Status != surface.StatusUnknown {
		t.Fatalf("a thread Codex has not loaded was read past the tail window: status=%s activity=%+v", unloaded.Status, unloaded.Activity)
	}
}

type countingReaderAt struct {
	reader *bytes.Reader
	read   int64
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.reader.ReadAt(p, off)
	c.read += int64(n)
	return n, err
}

func TestTheFirstReadOfARolloutStopsAtItsNewestLifecycleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	at := time.Now().Add(-time.Hour)
	appendCodexFiller(t, path, at, 3<<20)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	appendLine(t, path, codexRolloutRecord(at, "event_msg", `{"type":"task_started"}`))
	appendCodexFiller(t, path, at, 6<<20)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len(content))

	reader := &countingReaderAt{reader: bytes.NewReader(content)}
	scan := lastCodexLifecycle(reader, size, size)
	if !scan.found || scan.offset != before.Size() {
		t.Fatalf("found=%t offset=%d want %d", scan.found, scan.offset, before.Size())
	}
	if span := size - scan.offset; reader.read > span+codexStatusReadChunk || reader.read != scan.read {
		t.Fatalf("read %d bytes to find a record %d bytes from the end", reader.read, span)
	}

	bounded := &countingReaderAt{reader: bytes.NewReader(content)}
	if scan := lastCodexLifecycle(bounded, size, codexStatusReadLimit); scan.found || bounded.read != codexStatusReadLimit {
		t.Fatalf("a bounded read found=%t after %d bytes", scan.found, bounded.read)
	}
}

func TestAnIdleCodexThreadKeepsItsRecency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-synthetic.jsonl")
	ended := time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)
	appendLine(t, path, codexRolloutRecord(ended.Add(-time.Minute), "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(ended, "event_msg", `{"type":"task_complete"}`))
	recency := ended.Add(time.Hour)
	session := codexWorkingSession(path, surface.StatusIdle)
	session.LastActive = recency
	(&Codex{}).reconcileLocalStatus(context.Background(), &session)
	if session.Status != surface.StatusIdle || session.Activity != nil || !session.LastActive.Equal(recency) {
		t.Fatalf("status=%s activity=%+v last=%s want %s", session.Status, session.Activity, session.LastActive, recency)
	}
}

func TestACodexSessionsLastActivityNeverMovesBackAcrossAReplacedRollout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-synthetic.jsonl")
	started := time.Now().Add(-10 * time.Minute).Truncate(time.Millisecond)
	newest := started.Add(9 * time.Minute)
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	appendLine(t, path, codexRolloutRecord(newest, "event_msg", `{"type":"token_count"}`))
	codex := &Codex{}
	first := codexWorkingSession(path, surface.StatusBusy)
	first.LastActive = started
	codex.reconcileLocalStatus(context.Background(), &first)
	if first.LastActive.Before(newest) {
		t.Fatalf("last active %s is older than %s", first.LastActive, newest)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	appendLine(t, path, codexRolloutRecord(started, "event_msg", `{"type":"task_started"}`))
	if err := os.Chtimes(path, started, started); err != nil {
		t.Fatal(err)
	}
	again := codexWorkingSession(path, surface.StatusBusy)
	again.LastActive = started
	codex.reconcileLocalStatus(context.Background(), &again)
	if !again.LastActive.Equal(first.LastActive) {
		t.Fatalf("a replaced rollout moved last activity back to %s from %s", again.LastActive, first.LastActive)
	}
}
