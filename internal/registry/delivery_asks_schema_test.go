package registry

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARegistryAtSchema91MovesDeliveryProblemsToAsks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "sender", "target")
	open := failIntent(t, r, DeliveryIntentSent, surface.EvidenceTransportAccepted)
	dismissed := failIntent(t, r, DeliveryIntentSent, surface.EvidenceTransportAccepted)
	attended, err := r.QueueMessageWithKey("target", "unconfirmed", "unconfirmed:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE message_queue SET status='inflight',attempts=1 WHERE id=?`, attended); err != nil {
		t.Fatal(err)
	}
	if err := r.DeadLetterUnknown(attended, errors.New("connection closed")); err != nil {
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, r)
	for _, statement := range []string{
		`DELETE FROM asks`,
		`CREATE TABLE attention_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			queue_id INTEGER NOT NULL UNIQUE REFERENCES message_queue(id) ON DELETE CASCADE,
			reason TEXT NOT NULL,
			requested_action TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at TEXT NOT NULL DEFAULT '',
			resolution TEXT NOT NULL DEFAULT '')`,
		`CREATE INDEX attention_items_open ON attention_items(resolved_at, created_at DESC, id DESC)`,
		`DROP INDEX delivery_intents_status_updated`,
		`ALTER TABLE delivery_intents ADD COLUMN dismissed_at TEXT`,
		`ALTER TABLE delivery_intents ADD COLUMN notification_queue_id INTEGER REFERENCES message_queue(id) ON DELETE SET NULL`,
		`ALTER TABLE message_queue ADD COLUMN refusal_announced INTEGER NOT NULL DEFAULT 0`,
		`PRAGMA user_version = 91`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET dismissed_at=datetime('now') WHERE id=?`, dismissed); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action) VALUES('target',?,'Delivery outcome could not be confirmed','Retry or cancel this message')`, attended); err != nil {
		t.Fatal(err)
	}
	r.Close()

	if current, err := OpenCurrent(path); err == nil {
		current.Close()
		t.Fatal("a hook opened a schema 91 registry that this build cannot read")
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	asks := map[string]Ask{}
	for _, session := range []string{"sender", "target"} {
		raised, err := r.SessionAsks(session)
		if err != nil {
			t.Fatal(err)
		}
		for _, ask := range raised {
			asks[ask.Ref] = ask
		}
	}
	if len(asks) != 3 {
		t.Fatalf("asks=%+v, want one per problem", asks)
	}
	for _, ask := range asks {
		ref := deliveryRef(t, ask)
		switch {
		case ref.IntentID == open:
			if ask.State != AskOpen || ask.SessionID != "sender" || ask.RoutedTo != AskToPerson {
				t.Fatalf("open failure ask=%+v, want it open and routed to the person", ask)
			}
		case ref.IntentID == dismissed:
			if ask.State != AskAnswered || ask.AnsweredBy != AskToPerson || ask.Answer.Answers[ask.Questions[0].Question] != "Dismiss" {
				t.Fatalf("dismissed failure ask=%+v, want it answered with Dismiss", ask)
			}
		case ref.QueueID == attended:
			if ask.State != AskOpen || ask.SessionID != "target" || ask.RoutedTo != AskToPerson {
				t.Fatalf("attention item ask=%+v, want it open and routed to the person", ask)
			}
		default:
			t.Fatalf("unexpected ask %+v", ask)
		}
	}

	var tables int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='attention_items'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("attention_items found=%d err=%v", tables, err)
	}
	for _, gone := range []struct{ table, column string }{
		{"delivery_intents", "dismissed_at"},
		{"delivery_intents", "notification_queue_id"},
		{"message_queue", "refusal_announced"},
	} {
		var found int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, gone.table, gone.column).Scan(&found); err != nil || found != 0 {
			t.Fatalf("%s.%s found=%d err=%v", gone.table, gone.column, found, err)
		}
	}
	for _, index := range []string{"delivery_intents_provider_key", "delivery_intents_uuid", "delivery_intents_sender_status", "delivery_intents_target", "delivery_intents_status_updated"} {
		var found int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='delivery_intents' AND name=?`, index).Scan(&found); err != nil || found != 1 {
			t.Fatalf("index %s found=%d err=%v", index, found, err)
		}
	}
	if intent, err := r.DeliveryIntent(open); err != nil || intent.Status != DeliveryIntentFailed || intent.Failure != "transport failed" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}
