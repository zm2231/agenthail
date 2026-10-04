package registry

import "testing"

func TestV10JournalStateWithoutSeedColumnsMigratesToSchema12(t *testing.T) {
	path := t.TempDir() + "/registry.db"
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, first, "session")
	if _, err := first.db.Exec(`
		INSERT INTO session_journal_state(session_id,next_seq,retained_bytes,source_epoch,pruned_before) VALUES('session',0,0,'epoch',0);
		ALTER TABLE session_journal_state DROP COLUMN seed_status;
		ALTER TABLE session_journal_state DROP COLUMN seed_seq;
		ALTER TABLE session_journal_state DROP COLUMN seed_identity;
		PRAGMA user_version=10;`); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var version int
	if err := second.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 12 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	status, seq, identity, err := second.SessionJournalSeedCheckpoint("session")
	if err != nil || status != SessionJournalSeedUnknown || seq != 0 || identity != "" {
		t.Fatalf("checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
	if err := second.MarkSessionJournalSeed("session", true); err != nil {
		t.Fatal(err)
	}
	status, seq, identity, err = second.SessionJournalSeedCheckpoint("session")
	if err != nil || status != SessionJournalSeeded || seq != 0 || identity != "" {
		t.Fatalf("seeded checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
}

func TestV11SuccessfulSeedWithoutIdentityMigratesAsUntrustedCheckpoint(t *testing.T) {
	path := t.TempDir() + "/registry.db"
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, first, "session")
	entry, changed, err := first.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "text", ProviderKey: "timeline:old", Payload: []byte(`{"itemId":"old","kind":"text","body":"old generation"}`)}, SessionJournalRetention{Count: 10, Bytes: 1024})
	if err != nil || !changed || entry.Seq != 1 {
		t.Fatalf("entry=%+v changed=%v err=%v", entry, changed, err)
	}
	if err := first.MarkSessionJournalSeed("session", true); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`ALTER TABLE session_journal_state DROP COLUMN seed_identity; PRAGMA user_version=11`); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	status, seq, identity, err := second.SessionJournalSeedCheckpoint("session")
	if err != nil || status != SessionJournalSeeded || seq != 1 || identity != "" {
		t.Fatalf("checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
}
