package registry

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func TestQueuePreservesSourceSessionIDAcrossReopenClaimAndExports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path() != path {
		t.Fatalf("registry path=%q, want %q", r.Path(), path)
	}
	if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindClaude}); err != nil {
		t.Fatal(err)
	}
	queueID, err := r.QueueMessageWithOptions("target", "hello", "", surface.SendOptions{Model: "sonnet", SourceSessionID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	item, err := r.QueueItem(queueID)
	if err != nil || item.SourceSessionID != "source" {
		t.Fatalf("queue item=%+v err=%v", item, err)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].SourceSessionID != "source" {
		t.Fatalf("queue rows=%+v err=%v", rows, err)
	}
	payload, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || !strings.Contains(string(payload), `"sourceSessionId":"source"`) {
		t.Fatalf("queue export=%s", payload)
	}
	claimed, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || claimed == nil || claimed.SourceSessionID != "source" || claimed.Model != "sonnet" {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
}

func TestV1UpgradeAddsSourceColumnWithoutLegacyNormalization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO sessions(id,surface) VALUES('target','claude'); INSERT INTO message_queue(session_id,message,status,delivered,expires_at_ms) VALUES('target','legacy','',1,0); ALTER TABLE message_queue DROP COLUMN source_session_id; PRAGMA user_version=1`); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var status string
	if err := r.db.QueryRow(`SELECT status FROM message_queue WHERE message='legacy'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		t.Fatalf("legacy status=%q was normalized during v1 upgrade", status)
	}
	var source string
	if err := r.db.QueryRow(`SELECT source_session_id FROM message_queue WHERE message='legacy'`).Scan(&source); err != nil || source != "" {
		t.Fatalf("source=%q err=%v", source, err)
	}
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestClaudeMergePreservesQueuedSenderIdentity(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, s := range []surface.Session{{ID: "target", Surface: surface.KindNotion}, {ID: "local", Surface: surface.KindClaude, Transcript: "/fixture/session.jsonl"}} {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	id, err := r.QueueMessageWithOptions("target", "hello", "", surface.SendOptions{SourceSessionID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "bridge", Surface: surface.KindClaude, Transcript: "/fixture/session.jsonl"}); err != nil {
		t.Fatal(err)
	}
	queued, err := r.QueueItem(id)
	if err != nil || queued.SourceSessionID != "bridge" {
		t.Fatalf("queued=%+v err=%v", queued, err)
	}
}
