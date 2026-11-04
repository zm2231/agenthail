package registry

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var journalTestRetention = SessionJournalRetention{Count: 2048, Bytes: 8 << 20}

func journalCompressFixture(t *testing.T) (*Registry, string) {
	t.Helper()
	r := openTestRegistry(t)
	session := "compress-me"
	if err := r.RegisterSession(surface.Session{ID: session, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir(), Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	return r, session
}

func journalPayload(t *testing.T, kind, key, body string, extra map[string]any) []byte {
	t.Helper()
	fields := map[string]any{"itemId": key, "providerKey": key, "op": "upsert", "version": 1, "kind": kind, "ts": "2026-10-01T00:00:00Z", "body": body, "truncated": false}
	for name, value := range extra {
		fields[name] = value
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func appendJournalRow(t *testing.T, r *Registry, session, kind, key, body string, at time.Time, extra map[string]any, full []byte) SessionJournalEntry {
	t.Helper()
	entry := SessionJournalEntry{SessionID: session, Kind: kind, ProviderKey: key, Payload: journalPayload(t, kind, key, body, extra), ObservedAt: at, FullBody: full}
	if len(full) > 0 {
		entry.BodyRef = "ref-" + key
	}
	stored, _, err := r.AppendSessionJournalEntry(entry, journalTestRetention)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func fillJournal(t *testing.T, r *Registry, session string, at time.Time) {
	t.Helper()
	long := strings.Repeat("the same tool output, over and over. ", 200)
	appendJournalRow(t, r, session, "message", "m1", "a person's prompt "+long, at, map[string]any{"role": "user", "origin": "human"}, nil)
	appendJournalRow(t, r, session, "toolCall", "c1", long, at, nil, nil)
	appendJournalRow(t, r, session, "toolResult", "r1", long, at, nil, []byte("a full body kept apart "+long))
	appendJournalRow(t, r, session, "event", "e1", long, at, nil, nil)
	appendJournalRow(t, r, session, "text", "t1", long, at, nil, nil)
}

func storedCodecs(t *testing.T, r *Registry, session string) map[string]int {
	t.Helper()
	rows, err := r.read.Query(`SELECT provider_key,payload_codec FROM session_journal WHERE session_id=?`, session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	codecs := map[string]int{}
	for rows.Next() {
		var key string
		var codec int
		if err := rows.Scan(&key, &codec); err != nil {
			t.Fatal(err)
		}
		codecs[key] = codec
	}
	return codecs
}

func TestACompressedJournalReadsTheSameThroughEveryReader(t *testing.T) {
	r, session := journalCompressFixture(t)
	fillJournal(t, r, session, time.Now().Add(-30*24*time.Hour))
	type view struct {
		Page    SessionJournalPage
		After   SessionJournalWindow
		ByKey   SessionJournalEntry
		Body    []byte
		Prompts []JournalPrompt
	}
	read := func() view {
		page, err := r.ReadSessionJournalPage(session, 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		after, err := r.SessionJournalAfter(session, 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		byKey, found, err := r.SessionJournalEntryByProviderKey(session, "r1")
		if err != nil || !found {
			t.Fatalf("by key: %v %v", found, err)
		}
		body, _, err := r.SessionJournalBody(session, "ref-r1", 0, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		prompts, err := r.JournalPrompts(JournalPromptQuery{SessionID: session, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return view{page, after, byKey, body, prompts}
	}
	before := read()
	var retainedBefore int64
	if err := r.read.QueryRow(`SELECT retained_bytes FROM session_journal_state WHERE session_id=?`, session).Scan(&retainedBefore); err != nil {
		t.Fatal(err)
	}

	compressed, err := r.CompressJournalSession(session)
	if err != nil {
		t.Fatal(err)
	}
	if compressed.Rows != 3 || compressed.Packed >= compressed.Plain {
		t.Fatalf("compressed=%+v", compressed)
	}
	if got := storedCodecs(t, r, session); !reflect.DeepEqual(got, map[string]int{"m1": 0, "t1": 0, "c1": 1, "r1": 1, "e1": 1}) {
		t.Fatalf("codecs=%v", got)
	}
	if after := read(); !reflect.DeepEqual(before, after) {
		t.Fatalf("reads changed:\nbefore %+v\nafter  %+v", before, after)
	}
	var retainedAfter int64
	if err := r.read.QueryRow(`SELECT retained_bytes FROM session_journal_state WHERE session_id=?`, session).Scan(&retainedAfter); err != nil {
		t.Fatal(err)
	}
	if retainedBefore != retainedAfter {
		t.Fatalf("retained bytes moved from %d to %d, which would change retention", retainedBefore, retainedAfter)
	}
}

func TestOnlySessionsIdlePastTheCutoffWithUnsweptRowsAreCandidates(t *testing.T) {
	r, session := journalCompressFixture(t)
	now := time.Now()
	cutoff := now.Add(-7 * 24 * time.Hour)
	fillJournal(t, r, session, now.Add(-8*24*time.Hour))
	candidates := func() []string {
		t.Helper()
		ids, err := r.IdleJournalSessions(cutoff)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got := candidates(); len(got) != 1 || got[0] != session {
		t.Fatalf("idle session: candidates=%v", got)
	}
	if _, err := r.CompressJournalSession(session); err != nil {
		t.Fatal(err)
	}
	if got := candidates(); len(got) != 0 {
		t.Fatalf("after a sweep: candidates=%v", got)
	}
	second, err := r.CompressJournalSession(session)
	if err != nil || second.Rows != 0 {
		t.Fatalf("second sweep=%+v err=%v", second, err)
	}

	appendJournalRow(t, r, session, "toolCall", "c2", strings.Repeat("new work ", 200), now, nil, nil)
	if got := candidates(); len(got) != 0 {
		t.Fatalf("a session with new activity must wait: candidates=%v", got)
	}
	later, err := r.IdleJournalSessions(now.Add(time.Hour))
	if err != nil || len(later) != 1 {
		t.Fatalf("idle again: %v %v", later, err)
	}
}

func TestRowsThatDoNotShrinkStopBeingCandidates(t *testing.T) {
	r, session := journalCompressFixture(t)
	appendJournalRow(t, r, session, "toolCall", "tiny", "x", time.Now().Add(-30*24*time.Hour), nil, nil)
	if _, err := r.db.Exec(`UPDATE session_journal SET payload=X'7B7D' WHERE session_id=?`, session); err != nil {
		t.Fatal(err)
	}
	compressed, err := r.CompressJournalSession(session)
	if err != nil || compressed.Rows != 0 {
		t.Fatalf("compressed=%+v err=%v", compressed, err)
	}
	ids, err := r.IdleJournalSessions(time.Now())
	if err != nil || len(ids) != 0 {
		t.Fatalf("candidates=%v err=%v", ids, err)
	}
}

func TestAnUpsertDuringASweepKeepsItsNewContent(t *testing.T) {
	r, session := journalCompressFixture(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	fillJournal(t, r, session, old)
	packed, err := r.packJournalRows(session)
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Repeat("rewritten while the sweep ran ", 100)
	moved := appendJournalRow(t, r, session, "toolCall", "c1", newer, time.Now(), nil, nil)
	summary, err := r.storePackedJournalRows(packed)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Rows != 2 {
		t.Fatalf("stored %d rows, want the 2 that did not move", summary.Rows)
	}
	if got := storedCodecs(t, r, session)["c1"]; got != journalCodecPlain {
		t.Fatalf("the moved row has codec %d", got)
	}
	read, found, err := r.SessionJournalEntryByProviderKey(session, "c1")
	if err != nil || !found || !bytes.Equal(read.Payload, moved.Payload) {
		t.Fatalf("found=%v err=%v payload changed", found, err)
	}
	ids, err := r.IdleJournalSessions(time.Now().Add(time.Hour))
	if err != nil || len(ids) != 1 {
		t.Fatalf("the moved row is due another sweep: %v %v", ids, err)
	}
	summary, err = r.CompressJournalSession(session)
	if err != nil || summary.Rows != 1 {
		t.Fatalf("next sweep=%+v err=%v", summary, err)
	}
}

func TestAnUpsertOfACompressedRow(t *testing.T) {
	r, session := journalCompressFixture(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	fillJournal(t, r, session, old)
	if _, err := r.CompressJournalSession(session); err != nil {
		t.Fatal(err)
	}
	same, found, err := r.SessionJournalEntryByProviderKey(session, "c1")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	_, changed, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: session, Kind: "toolCall", ProviderKey: "c1", Payload: same.Payload}, journalTestRetention)
	if err != nil || changed {
		t.Fatalf("unchanged content rewrote the row: changed=%v err=%v", changed, err)
	}
	if got := storedCodecs(t, r, session)["c1"]; got != journalCodecZstd {
		t.Fatalf("unchanged content stored codec %d", got)
	}
	appendJournalRow(t, r, session, "toolCall", "c1", "changed", time.Now(), nil, nil)
	if got := storedCodecs(t, r, session)["c1"]; got != journalCodecPlain {
		t.Fatalf("new content stored codec %d", got)
	}
	read, _, err := r.SessionJournalEntryByProviderKey(session, "c1")
	if err != nil || !strings.Contains(string(read.Payload), "changed") {
		t.Fatalf("payload=%s err=%v", read.Payload, err)
	}
}

func TestARebuildWritesPlainRowsAndTheNextSweepCompressesThemOnce(t *testing.T) {
	r, session := journalCompressFixture(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	fillJournal(t, r, session, old)
	if _, err := r.CompressJournalSession(session); err != nil {
		t.Fatal(err)
	}
	window, err := r.SessionJournalAfter(session, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	entries := window.Entries
	for index := range entries {
		if entries[index].BodyRef == "" {
			continue
		}
		full, found, err := r.SessionJournalEntryByProviderKey(session, entries[index].ProviderKey)
		if err != nil || !found {
			t.Fatalf("found=%v err=%v", found, err)
		}
		entries[index].FullBody = full.FullBody
	}
	if _, err := r.ReplaceSessionJournal(session, entries, journalTestRetention, 1); err != nil {
		t.Fatal(err)
	}
	for key, codec := range storedCodecs(t, r, session) {
		if codec != journalCodecPlain {
			t.Fatalf("%s has codec %d after a rebuild", key, codec)
		}
	}
	first, err := r.CompressJournalSession(session)
	if err != nil || first.Rows != 3 {
		t.Fatalf("first sweep=%+v err=%v", first, err)
	}
	second, err := r.CompressJournalSession(session)
	if err != nil || second.Rows != 0 {
		t.Fatalf("second sweep=%+v err=%v", second, err)
	}
	ids, err := r.IdleJournalSessions(time.Now().Add(time.Hour))
	if err != nil || len(ids) != 0 {
		t.Fatalf("candidates=%v err=%v", ids, err)
	}
}

func TestCompressionLeavesRetentionWhereItWas(t *testing.T) {
	r, session := journalCompressFixture(t)
	old := time.Now().Add(-30 * 24 * time.Hour)
	body := strings.Repeat("kept until the byte budget says otherwise ", 100)
	size := 0
	for i := range 4 {
		size = appendJournalRow(t, r, session, "toolCall", "c"+strconv.Itoa(i), body, old, nil, nil).Bytes
	}
	if _, err := r.CompressJournalSession(session); err != nil {
		t.Fatal(err)
	}
	tight := SessionJournalRetention{Count: 2048, Bytes: size * 4}
	payload := journalPayload(t, "toolCall", "c9", body, nil)
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: session, Kind: "toolCall", ProviderKey: "c9", Payload: payload}, tight); err != nil {
		t.Fatal(err)
	}
	if _, found, err := r.SessionJournalEntryByProviderKey(session, "c0"); err != nil || found {
		t.Fatalf("the oldest row should be trimmed by its logical size: found=%v err=%v", found, err)
	}
	if _, found, err := r.SessionJournalEntryByProviderKey(session, "c1"); err != nil || !found {
		t.Fatalf("only the oldest should go: found=%v err=%v", found, err)
	}
}

func TestADamagedOrUnknownRowIsAnErrorNotGarbage(t *testing.T) {
	r, session := journalCompressFixture(t)
	fillJournal(t, r, session, time.Now())
	for name, change := range map[string]string{
		"damaged": `UPDATE session_journal SET payload=X'00',payload_codec=1 WHERE session_id=? AND provider_key='c1'`,
		"unknown": `UPDATE session_journal SET payload_codec=9 WHERE session_id=? AND provider_key='c1'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.db.Exec(change, session); err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.SessionJournalEntryByProviderKey(session, "c1"); err == nil {
				t.Fatal("the row read as if fine")
			}
		})
	}
}

func TestANewRegistryCanGiveFreedPagesBackToTheDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var mode int
	if err := r.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != autoVacuumIncremental {
		t.Fatalf("auto_vacuum=%d err=%v", mode, err)
	}
	if _, err := r.db.Exec(`CREATE TABLE filler(b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		if _, err := r.db.Exec(`INSERT INTO filler VALUES(randomblob(1048576))`); err != nil {
			t.Fatal(err)
		}
	}
	checkpoint := func() int64 {
		t.Helper()
		if _, err := r.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	full := checkpoint()
	if _, err := r.db.Exec(`DELETE FROM filler`); err != nil {
		t.Fatal(err)
	}
	for {
		left, err := r.ReclaimFreePages()
		if err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
	}
	if shrunk := checkpoint(); shrunk > full/2 {
		t.Fatalf("the file went from %d to %d", full, shrunk)
	}
}

func TestAnUpgradedRegistryKeepsItsVacuumModeAndGainsTheColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA auto_vacuum=NONE`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_journal_old(id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion-1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var mode int
	if err := r.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != 0 {
		t.Fatalf("auto_vacuum=%d err=%v: an existing file must not be converted", mode, err)
	}
	if left, err := r.ReclaimFreePages(); err != nil || left != 0 {
		t.Fatalf("left=%d err=%v", left, err)
	}
	for table, column := range map[string]string{"session_journal": "payload_codec", "session_journal_state": "swept_seq"} {
		var count int
		if err := r.read.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s.%s count=%d err=%v", table, column, count, err)
		}
	}
}
