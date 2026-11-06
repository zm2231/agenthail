package registry

import (
	"strconv"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestOpenMovesEachHostsLedgerCaptureIntoTheReplicatedLog(t *testing.T) {
	r := openTestRegistry(t)
	since := time.Date(2026, 11, 2, 12, 57, 34, 123e6, time.UTC)
	for _, step := range []string{
		`ALTER TABLE ledger_state ADD COLUMN enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE ledger_state ADD COLUMN since_ms INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO ledger_state(project_key,enabled,since_ms,owner_host_id) VALUES('on',1,` + strconv.FormatInt(since.UnixMilli(), 10) + `,'hother')`,
		`INSERT INTO ledger_state(project_key,enabled) VALUES('off',2)`,
		`INSERT INTO ledger_state(project_key,enabled,owner_host_id) VALUES('owned','0','hother')`,
		`PRAGMA user_version=52`,
	} {
		if _, err := r.db.Exec(step); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if err := r.migrate(true); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(r.db.raw, r.db.raw)
	for key, want := range map[string]ledger.Scope{
		"on":    {State: ledger.LedgerEnabled, Since: since},
		"off":   {State: ledger.LedgerDisabled},
		"owned": {State: ledger.LedgerUnset},
	} {
		if got, err := store.Scope(key); err != nil || got.State != want.State || !got.Since.Equal(want.Since) {
			t.Fatalf("%s scope = %+v err %v, want %+v", key, got, err, want)
		}
	}
	var self string
	if err := r.db.QueryRow(`SELECT host_id FROM host_identity WHERE id=1`).Scan(&self); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingReplay("on", 10)
	if err != nil || len(pending) != 1 || pending[0].ActorHostID != self || pending[0].OriginSeq != pending[0].Seq {
		t.Fatalf("the enable does not wait to replay to the owner: %+v err %v", pending, err)
	}
	for _, column := range []string{"enabled", "since_ms"} {
		var n int
		if err := r.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ledger_state') WHERE name=?`, column).Scan(&n); err != nil || n != 0 {
			t.Fatalf("ledger_state.%s still there: %d %v", column, n, err)
		}
	}
	var owner string
	if err := r.db.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key='owned'`).Scan(&owner); err != nil || owner != "hother" {
		t.Fatalf("the owner assignment was lost: %q %v", owner, err)
	}
}
