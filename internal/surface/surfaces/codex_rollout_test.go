package surfaces

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

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
	session := &surface.Session{ID: "worker", Transcript: old, TranscriptIdentity: transcriptFileIdentity(info), TranscriptOffset: info.Size(), TranscriptOffsetSet: true}
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
	if session.Transcript != next || session.TranscriptIdentity == transcriptFileIdentity(info) {
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
	if current.Transcript != indexed || page.UnavailableReason != "" || len(page.Items) == 0 || !codex.ValidateTranscriptContinuation(previous, current) {
		t.Fatalf("installed continuation not adopted: path=%s items=%d unavailable=%s", current.Transcript, len(page.Items), page.UnavailableReason)
	}
	t.Logf("read-only installed continuation verified: thread=%s items=%d offset=%d", threadID, len(page.Items), page.TranscriptOffset)
}
