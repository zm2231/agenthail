package registry

import (
	"encoding/json"
	"github.com/zm2231/agenthail/internal/surface"
	"path/filepath"
	"testing"
	"time"
)

func TestTurnOptionsSurviveV2MigrationAndQueueReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec("ALTER TABLE message_queue DROP COLUMN turn_options"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec("PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	r.Close()
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	options := surface.TurnOptions{Effort: "xhigh", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}
	id, err := r.QueueMessageWithOptions("target", "hello", "", surface.SendOptions{SourceSessionID: "sender", TurnOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	row, err := r.QueueItem(id)
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("target", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []surface.TurnOptions{rows[0].TurnOptions, row.TurnOptions, item.TurnOptions} {
		data, _ := json.Marshal(got)
		want, _ := json.Marshal(options)
		if string(data) != string(want) {
			t.Fatalf("got=%s want=%s", data, want)
		}
	}
	encoded, _ := json.Marshal(row)
	if !json.Valid(encoded) {
		t.Fatal(string(encoded))
	}
}
