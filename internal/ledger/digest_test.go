package ledger_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func digestOf(t *testing.T, s *ledger.Store, through int64) ledger.LogDigest {
	t.Helper()
	digest, err := s.LogDigest(project, through)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func stampOf(t *testing.T, db *sql.DB, key, ref string) string {
	t.Helper()
	var stamp string
	if err := db.QueryRow(`SELECT created_at FROM ledger_events WHERE project_key=? AND ref=?`, key, ref).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func TestAStampInAnotherFormatTravelsUnchangedBothWays(t *testing.T) {
	const other = "repo:github.com/acme/infra"
	mac, owner := newSyncHost(t), newSyncHost(t)
	stamp := func(db *sql.DB, key, ref string) {
		if _, err := db.Exec(`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,ref) VALUES(?,?,?,?,?)`, key, string(ledger.EventUtterance), `{"text":"stamped by the column default","utteranceId":"`+ref+`"}`, "s1", ref); err != nil {
			t.Fatal(err)
		}
	}
	stamp(mac.db, project, "claude-prompt:from-mac")
	stamp(owner.db, other, "claude-prompt:from-owner")
	adopt(t, project, mac, owner)
	adopt(t, other, mac, owner)

	for key, ref := range map[string]string{project: "claude-prompt:from-mac", other: "claude-prompt:from-owner"} {
		replica, authoritative := stampOf(t, mac.db, key, ref), stampOf(t, owner.db, key, ref)
		if replica != authoritative || strings.Contains(replica, "T") {
			t.Fatalf("%s: created_at %q on the replica, %q on the owner", ref, replica, authoritative)
		}
		through, err := mac.store.DigestThrough(key)
		if err != nil {
			t.Fatal(err)
		}
		mine, err := mac.store.LogDigest(key, through)
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := owner.store.LogDigest(key, through)
		if err != nil || mine != theirs || mine.Count != 1 {
			t.Fatalf("%s: the logs disagree: replica %+v, owner %+v (%v)", key, mine, theirs, err)
		}
	}
}

func TestSyncedHostsAgreeOnTheLogDigestUntilARowDiffers(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	adopt(t, project, mac, owner)
	capture(t, owner.store, "repo:github.com/acme/infra", "claude-prompt:elsewhere", "another project's words between this one's")
	items(t, owner.store, "add the index")
	capture(t, mac.store, project, "claude-prompt:later", "and later")
	syncOnce(t, project, mac, owner)

	through, err := mac.store.DigestThrough(project)
	if err != nil {
		t.Fatal(err)
	}
	ownerThrough, err := owner.store.DigestThrough(project)
	if err != nil || ownerThrough != through {
		t.Fatalf("the owner's log ends at %d, the replica's cursor at %d (%v)", ownerThrough, through, err)
	}
	replica, authoritative := digestOf(t, mac.store, through), digestOf(t, owner.store, through)
	if replica != authoritative || replica.Count == 0 {
		t.Fatalf("synced logs disagree: replica %+v, owner %+v", replica, authoritative)
	}

	var changed int64
	if err := mac.db.QueryRow(`SELECT owner_seq FROM ledger_events WHERE project_key=? AND owner_seq>0 ORDER BY owner_seq LIMIT 1 OFFSET 1`, project).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if _, err := mac.db.Exec(`UPDATE ledger_events SET payload=CAST(payload AS TEXT)||' ' WHERE project_key=? AND owner_seq=?`, project, changed); err != nil {
		t.Fatal(err)
	}
	if digestOf(t, mac.store, changed-1) != digestOf(t, owner.store, changed-1) {
		t.Fatal("the logs disagree before the changed row")
	}
	if digestOf(t, mac.store, changed) == digestOf(t, owner.store, changed) {
		t.Fatal("a changed payload kept the digest")
	}
}
