package surfaces

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexCheckpointWalkHonorsCancellationAndEntryLimit(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 4; index++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprint(index)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := walkCodexCheckpoints(ctx, root, 10, func(string, os.DirEntry) error { t.Fatal("visited after cancellation"); return nil }); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	visits := 0
	if err := walkCodexCheckpoints(context.Background(), root, 2, func(string, os.DirEntry) error { visits++; return nil }); err == nil || visits != 2 {
		t.Fatalf("entry cap: visits=%d error=%v", visits, err)
	}
}

func rolloutIndexFixture(t *testing.T, sessionID, path string) *sql.DB {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT); INSERT INTO threads VALUES(?,?)", sessionID, path); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCodexCheckpointRetainsPathAndRejectsReplacedSource(t *testing.T) {
	old := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	next := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	rolloutIndexFixture(t, "worker", next)
	oldInfo, _ := os.Stat(old)
	nextInfo, _ := os.Stat(next)
	previous := surface.Session{ID: "worker", Transcript: next, TranscriptIdentity: codexTranscriptIdentity(old, oldInfo)}
	current := surface.Session{ID: "worker", Transcript: next, TranscriptIdentity: codexTranscriptIdentity(next, nextInfo)}
	codex := NewCodex("")
	if !codex.ValidateTranscriptContinuation(context.Background(), previous, current) {
		t.Fatal("catalog path erased checkpoint provenance")
	}
	replacement := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	if err := os.Rename(replacement, old); err != nil {
		t.Fatal(err)
	}
	if codex.ValidateTranscriptContinuation(context.Background(), previous, current) {
		t.Fatal("replaced checkpoint source accepted")
	}
}

func TestCodexLegacyCheckpointRequiresPaginatedHistoryBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, "sessions", "2026", "10", "10")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	old, next := filepath.Join(directory, "rollout-worker-old.jsonl"), filepath.Join(directory, "rollout-worker-new.jsonl")
	meta := "{\"type\":\"session_meta\",\"ordinal\":0,\"payload\":{\"id\":\"worker\"}}\n"
	if err := os.WriteFile(old, []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := index.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,rollout_path TEXT); INSERT INTO threads VALUES('worker',?)", next); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(old)
	_, inode, _ := strings.Cut(transcriptFileIdentity(info), ":")
	previous := surface.Session{ID: "worker", Transcript: next, TranscriptIdentity: "old-device:" + inode}
	for _, variant := range []struct {
		name, thread string
		ordinal      int
		valid        bool
	}{{"same-thread", "worker", 1, true}, {"wrong-boundary", "worker", 2, false}, {"fork", "parent", 1, false}} {
		t.Run(variant.name, func(t *testing.T) {
			line := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\",\"history_base\":{\"thread_id\":%q,\"end_byte_offset\":%d,\"end_ordinal_exclusive\":%d}}}\n", variant.thread, len(meta), variant.ordinal)
			if err := os.WriteFile(next, []byte(line), 0600); err != nil {
				t.Fatal(err)
			}
			nextInfo, _ := os.Stat(next)
			current := surface.Session{ID: "worker", Transcript: next, TranscriptIdentity: codexTranscriptIdentity(next, nextInfo)}
			if got := NewCodex("").ValidateTranscriptContinuation(context.Background(), previous, current); got != variant.valid {
				t.Fatalf("valid=%v want %v", got, variant.valid)
			}
		})
	}
}

func TestCodexCheckpointUpgradeOnSameIndexedFile(t *testing.T) {
	path := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	rolloutIndexFixture(t, "worker", path)
	info, _ := os.Stat(path)
	_, inode, _ := strings.Cut(transcriptFileIdentity(info), ":")
	previous := surface.Session{ID: "worker", Transcript: path, TranscriptIdentity: "old-device:" + inode}
	current := surface.Session{ID: "worker", Transcript: path, TranscriptIdentity: codexTranscriptIdentity(path, info)}
	if !NewCodex("").ValidateTranscriptContinuation(context.Background(), previous, current) {
		t.Fatal("same-file checkpoint upgrade refused")
	}
	previous = current
	if err := os.WriteFile(path, []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\",\"extra\":true}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	current.TranscriptIdentity = codexTranscriptIdentity(path, info)
	if NewCodex("").ValidateTranscriptContinuation(context.Background(), previous, current) {
		t.Fatal("changed same-file header accepted")
	}
}

func TestCodexRolloutIndexRequiresExactThreadIdentity(t *testing.T) {
	old := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	next := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"other\",\"session_id\":\"worker\"}}\n")
	db := rolloutIndexFixture(t, "worker", next)
	session := &surface.Session{ID: "worker", Transcript: old}
	if got := codexTranscriptPath(session); got != old {
		t.Fatalf("unrelated/subagent rollout selected: %s", got)
	}
	if err := os.WriteFile(next, []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := codexTranscriptPath(session); got != next {
		t.Fatalf("continuation=%s want %s", got, next)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM threads").Scan(&count); err != nil || count != 1 {
		t.Fatalf("index changed: count=%d err=%v", count, err)
	}
}

func TestCodexStreamFollowsIndexedContinuationWithoutReconnect(t *testing.T) {
	old := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n")
	next := timelineFixture(t, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"worker\"}}\n"+`{"type":"response_item","payload":{"type":"message","id":"new-answer","role":"assistant","content":[{"type":"output_text","text":"Live continuation answer"}]}}`+"\n")
	db := rolloutIndexFixture(t, "worker", old)
	info, _ := os.Stat(old)
	session := &surface.Session{ID: "worker", Transcript: old, TranscriptIdentity: codexTranscriptIdentity(old, info), TranscriptOffset: info.Size(), TranscriptOffsetSet: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events := make(chan surface.StreamEvent, 10)
	done := make(chan error, 1)
	go func() {
		done <- (&Codex{}).streamTranscript(ctx, session, "", func(event surface.StreamEvent) { events <- event }, time.Minute)
	}()
	if _, err := db.Exec("UPDATE threads SET rollout_path=? WHERE id='worker'", next); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Text != "Live continuation answer" {
			t.Fatal(fmt.Sprintf("event=%+v", event))
		}
	case err := <-done:
		t.Fatalf("stream stopped before continuation: %v", err)
	case <-ctx.Done():
		t.Fatal("live continuation never reached the stream")
	}
	cancel()
	<-done
	if session.Transcript != next || session.TranscriptIdentity == codexTranscriptIdentity(old, info) {
		t.Fatalf("continuation cursor not retained: %+v", session)
	}
}

func TestCodexInstalledRolloutRead(t *testing.T) {
	threadID, previousPath := os.Getenv("AGENTHAIL_CODEX_ROLLOUT_TEST_THREAD"), os.Getenv("AGENTHAIL_CODEX_ROLLOUT_TEST_PREVIOUS")
	if threadID == "" || previousPath == "" {
		t.Skip("requires an explicitly selected local thread and previous rollout; read-only")
	}
	indexed := indexedCodexRollout(codexHome(), threadID)
	if indexed == "" || indexed == previousPath {
		t.Fatal("no indexed continuation for the selected thread")
	}
	previousInfo, err := os.Stat(previousPath)
	if err != nil {
		t.Fatal(err)
	}
	previous := surface.Session{ID: threadID, Surface: surface.KindCodex, Transcript: previousPath, TranscriptIdentity: transcriptFileIdentity(previousInfo)}
	current := previous
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	codex := NewCodex("http://127.0.0.1:1")
	page, err := codex.ReadSession(ctx, &current, surface.SessionReadRequest{Limit: 30})
	if err != nil {
		t.Fatal(err)
	}
	current.TranscriptIdentity = page.TranscriptIdentity
	if current.Transcript != indexed || page.UnavailableReason != "" || len(page.Items) == 0 || !codex.ValidateTranscriptContinuation(context.Background(), previous, current) {
		t.Fatalf("installed continuation not adopted: path=%s items=%d unavailable=%s", current.Transcript, len(page.Items), page.UnavailableReason)
	}
	t.Logf("read-only installed continuation verified: thread=%s items=%d offset=%d", threadID, len(page.Items), page.TranscriptOffset)
}
