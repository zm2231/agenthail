package registry

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func assertJournalTotals(t *testing.T, r *Registry, sessionID string, wantRows int) {
	t.Helper()
	var rows, bytes, actualRows, actualBytes int
	err := r.db.QueryRow(`SELECT retained_rows,retained_bytes,(SELECT COUNT(*) FROM session_journal WHERE session_id=?1),(SELECT COALESCE(SUM(bytes),0) FROM session_journal WHERE session_id=?1) FROM session_journal_state WHERE session_id=?1`, sessionID).Scan(&rows, &bytes, &actualRows, &actualBytes)
	if err != nil {
		t.Fatal(err)
	}
	if rows != actualRows || bytes != actualBytes {
		t.Fatalf("journal state says %d rows and %d bytes, the journal holds %d rows and %d bytes", rows, bytes, actualRows, actualBytes)
	}
	if actualRows != wantRows {
		t.Fatalf("journal holds %d rows, want %d", actualRows, wantRows)
	}
}

func TestEveryJournalWriteKeepsTheRetainedRowCountExact(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 3, Bytes: 64}
	appendEntry := func(key, payload string) {
		t.Helper()
		if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(payload)}, retention); err != nil {
			t.Fatal(err)
		}
	}

	appendEntry("a", "1")
	appendEntry("", "keyless")
	assertJournalTotals(t, r, "session", 2)
	appendEntry("a", "replaced")
	assertJournalTotals(t, r, "session", 2)
	appendEntry("b", "2")
	appendEntry("c", "3")
	appendEntry("d", "4")
	assertJournalTotals(t, r, "session", 3)
	appendEntry("e", strings.Repeat("e", 64))
	assertJournalTotals(t, r, "session", 1)

	body := SessionJournalEntry{SessionID: "session", Kind: "text", ProviderKey: "body", BodyRef: "body-ref", Payload: []byte(`{"bodyRef":"body-ref"}`), FullBody: []byte("hello")}
	if _, _, err := r.AppendSessionJournalEntry(body, retention); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AppendSessionJournalBody(body, len(body.FullBody), []byte(" world"), retention); err != nil {
		t.Fatal(err)
	}
	assertJournalTotals(t, r, "session", 1)

	replacement := []SessionJournalEntry{
		{Kind: "item", ProviderKey: "r1", Payload: []byte("1")},
		{Kind: "item", ProviderKey: "r2", Payload: []byte("2")},
		{Kind: "item", ProviderKey: "r3", Payload: []byte("3")},
		{Kind: "item", ProviderKey: "r4", Payload: []byte("4")},
	}
	if _, err := r.ReplaceSessionJournal("session", replacement, retention, 1); err != nil {
		t.Fatal(err)
	}
	assertJournalTotals(t, r, "session", 3)
	if _, err := r.ReplaceSessionJournal("session", replacement, SessionJournalRetention{Count: 3, Bytes: 2}, 1); err != nil {
		t.Fatal(err)
	}
	assertJournalTotals(t, r, "session", 2)
	if _, err := r.RestartSessionJournal("session", nil, retention, 1, 0); err != nil {
		t.Fatal(err)
	}
	assertJournalTotals(t, r, "session", 0)

	appendEntry("after", "1")
	old := time.Now().Add(-time.Hour)
	if _, err := r.db.Exec(`UPDATE sessions SET ended=1 WHERE id='session'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE session_journal SET observed_at=? WHERE session_id='session'`, formatTimestamp(old)); err != nil {
		t.Fatal(err)
	}
	stale, err := r.StaleJournalSessions(time.Now())
	if err != nil || len(stale) != 1 {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
	if dropped, err := r.DropStaleJournal(stale[0], time.Now()); err != nil || !dropped {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
	assertJournalTotals(t, r, "session", 0)
}

func TestAnUpgradedRegistryCountsTheJournalRowsItAlreadyHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, old, "session")
	for i := 0; i < 5; i++ {
		if _, _, err := old.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: fmt.Sprint(i), Payload: []byte("x")}, SessionJournalRetention{Count: 10, Bytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`UPDATE session_journal_state SET retained_rows=0`,
		`DELETE FROM schema_migrations WHERE name='2026-11-24-tracked-journal-rows'`,
	} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	assertJournalTotals(t, r, "session", 5)
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "new", Payload: []byte("x")}, SessionJournalRetention{Count: 5, Bytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	assertJournalTotals(t, r, "session", 5)
}

// A journal write reads its retention totals on every call; reading the
// session's rows there made each write cost grow with the retained journal.
func TestAJournalWriteReadsItsRetentionWithoutVisitingTheJournal(t *testing.T) {
	r := openTestRegistry(t)
	for _, step := range queryPlan(t, r, journalRetainedSQL, "session") {
		if strings.Contains(step, "session_journal ") || strings.HasSuffix(step, "session_journal") {
			t.Fatalf("the retention read visits the journal rows: %q", step)
		}
	}
}
