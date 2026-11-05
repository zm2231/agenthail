package ledger_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/ledger"
)

type syncHost struct {
	db    *sql.DB
	store *ledger.Store
	id    string
}

func newSyncHost(t *testing.T) syncHost {
	t.Helper()
	db := openTestDB(t)
	var id string
	if err := db.QueryRow(`SELECT host_id FROM host_identity WHERE id=1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return syncHost{db: db, store: ledger.NewCachedStore(db, db), id: id}
}

func syncOnce(t *testing.T, key string, mac, owner syncHost) {
	t.Helper()
	pending, err := mac.store.PendingReplay(key, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) > 0 {
		seqs, err := owner.store.AcceptEvents(key, mac.id, pending)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		if err := mac.store.MarkReplayed(key, pending, seqs); err != nil {
			t.Fatal(err)
		}
	}
	for {
		_, cursor, err := mac.store.Owner(key)
		if err != nil {
			t.Fatal(err)
		}
		events, err := owner.store.ExportEvents(key, cursor, 2)
		if err != nil {
			t.Fatalf("export: %v", err)
		}
		if len(events) == 0 {
			return
		}
		if err := mac.store.ApplyPulled(key, owner.id, events); err != nil {
			t.Fatalf("pull: %v", err)
		}
	}
}

func adopt(t *testing.T, key string, mac, owner syncHost) {
	t.Helper()
	holds, err := mac.store.HoldsHistory(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.store.ClaimOwnership(key, mac.id, holds); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := mac.store.AssignOwner(key, owner.id); err != nil {
		t.Fatalf("assign: %v", err)
	}
	syncOnce(t, key, mac, owner)
}

func ownerLog(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	return logRows(t, db, `SELECT seq, actor_host_id, origin_seq, type, payload, actor_session_id, ref, created_at FROM ledger_events WHERE project_key=? ORDER BY seq`, key)
}

func replicaLog(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var unsynced int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND owner_seq=0`, key).Scan(&unsynced); err != nil || unsynced != 0 {
		t.Fatalf("replica holds %d events the owner has not sequenced (%v)", unsynced, err)
	}
	return logRows(t, db, `SELECT owner_seq, actor_host_id, origin_seq, type, payload, actor_session_id, ref, created_at FROM ledger_events WHERE project_key=? ORDER BY owner_seq`, key)
}

func logRows(t *testing.T, db *sql.DB, query, key string) string {
	t.Helper()
	rows, err := db.Query(query, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq, origin int64
		var host, typ, actor, ref, at string
		var payload []byte
		if err := rows.Scan(&seq, &host, &origin, &typ, &payload, &actor, &ref, &at); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d %s/%d %s %s %s %s %s", seq, host, origin, typ, payload, actor, ref, at))
	}
	return strings.Join(out, "\n")
}

func sameLedger(t *testing.T, key string, mac, owner syncHost) {
	t.Helper()
	if got, want := replicaLog(t, mac.db, key), ownerLog(t, owner.db, key); got != want {
		t.Fatalf("logs differ\nreplica:\n%s\nowner:\n%s", got, want)
	}
	macBoard, err := mac.store.Board(key)
	if err != nil {
		t.Fatal(err)
	}
	ownerBoard, err := owner.store.Board(key)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hostFree(t, macBoard), hostFree(t, ownerBoard); got != want {
		t.Fatalf("boards differ\nreplica %s\nowner   %s", got, want)
	}
	rebuilt, err := ledger.NewStore(mac.db, mac.db).Board(key)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hostFree(t, rebuilt), hostFree(t, ownerBoard); got != want {
		t.Fatalf("replica rebuilt from its log differs from the owner\nreplica %s\nowner   %s", got, want)
	}
}

var appliedSeq = regexp.MustCompile(`"applied":\d+,`)

func hostFree(t *testing.T, b *ledger.Board) string {
	t.Helper()
	return appliedSeq.ReplaceAllString(snapshot(t, b), "")
}

func TestAdoptedHistoryReplicatesOnceEvenWhenReplayIsRetried(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	items(t, mac.store, "add the index")
	work(t, mac.store, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "reviewer", Text: "mind the migration"})

	if err := owner.store.ClaimOwnership(project, mac.id, true); err != nil {
		t.Fatal(err)
	}
	if err := mac.store.AssignOwner(project, owner.id); err != nil {
		t.Fatal(err)
	}
	pending, err := mac.store.PendingReplay(project, 500)
	if err != nil || len(pending) == 0 {
		t.Fatalf("pending = %d, %v", len(pending), err)
	}
	first, err := owner.store.AcceptEvents(project, mac.id, pending)
	if err != nil {
		t.Fatal(err)
	}
	again, err := owner.store.AcceptEvents(project, mac.id, pending)
	if err != nil || fmt.Sprint(first) != fmt.Sprint(again) {
		t.Fatalf("a retried replay did not return the same owner seqs: %v vs %v (%v)", first, again, err)
	}
	syncOnce(t, project, mac, owner)
	syncOnce(t, project, mac, owner)
	sameLedger(t, project, mac, owner)

	var n int
	if err := owner.db.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=?`, project).Scan(&n); err != nil || n != len(pending) {
		t.Fatalf("owner holds %d events, want each of the %d once (%v)", n, len(pending), err)
	}
}

func TestWritesOnEitherHostLandOnceAndInOwnerOrder(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	adopt(t, project, mac, owner)

	work(t, owner.store, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"})
	capture(t, mac.store, project, "claude-prompt:offline", "and keep it fast")
	syncOnce(t, project, mac, owner)
	work(t, owner.store, ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: "widget-W1", SessionID: "worker"})
	syncOnce(t, project, mac, owner)
	work(t, mac.store, ledger.WorkRequest{Action: ledger.WorkLog, ItemID: "widget-W1", SessionID: "worker", Text: "started on the mac"})
	work(t, owner.store, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "lead", Text: "written on forge before the mac replayed"})
	if done, err := mac.store.ReclassifyUtterance(project, "claude-prompt:offline", "scheduled", time.Now()); err != nil || !done {
		t.Fatalf("reclassify: %v %v", done, err)
	}
	syncOnce(t, project, mac, owner)
	sameLedger(t, project, mac, owner)

	item, ok := board(t, mac.store).WorkItem("widget-W1")
	if !ok || entryKinds(item) != "created state:in_progress comment log" {
		t.Fatalf("mac board item = %+v", item)
	}
}

func TestTheOwnerTakesNoIDIssuingReplayOnceItsLedgerIsShared(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	adopt(t, project, mac, owner)
	work(t, owner.store, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "owner write"})
	rogue := ledger.Event{ProjectKey: project, Type: ledger.EventWorkItem, Payload: []byte(`{"itemId":"widget-W9","clauseId":"widget-C1","text":"x","createdBy":"s1"}`), OriginSeq: 999, CreatedAt: time.Now()}
	if _, err := owner.store.AcceptEvents(project, mac.id, []ledger.Event{rogue}); err == nil || !strings.Contains(err.Error(), "issues an ID") {
		t.Fatalf("ID-issuing replay after sharing: %v", err)
	}
	broken := ledger.Event{ProjectKey: project, Type: ledger.EventWorkComment, Payload: []byte(`{"itemId":`), OriginSeq: 1000, CreatedAt: time.Now()}
	if _, err := owner.store.AcceptEvents(project, mac.id, []ledger.Event{broken}); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("malformed replay: %v", err)
	}
	if _, err := owner.store.Board(project); err != nil {
		t.Fatalf("the owner's board after refused replays: %v", err)
	}
}

func TestANonOwnerIssuesNoIDsEvenForACheckStartedBeforeTheMove(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	g := searchAsk(t, mac.store)
	if _, err := mac.store.StartCheck(project, g, "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	adopt(t, project, mac, owner)
	_, err := mac.store.ReportCheck(ledger.CheckReport{ProjectKey: project, Run: "run-1", SessionID: "run-1", Missing: []ledger.Omission{{Breadth: "fuzzy matching", From: "claude-prompt:1"}}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "planned there") {
		t.Fatalf("a non-owner's check report: %v", err)
	}
	workRefused(t, mac.store, ledger.WorkRequest{Action: ledger.WorkAdd, ClauseID: "widget-C1", Text: "add the index"}, "planned there")
	for _, typ := range []ledger.EventType{ledger.EventCut, ledger.EventWorkItem} {
		var n int
		if err := mac.db.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND type=?`, project, string(typ)).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the non-owner wrote %d %s events (%v)", n, typ, err)
		}
	}
}

func TestAPullThatReordersRebuildsTheBoardInOwnerOrder(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	items(t, mac.store, "add the index")
	adopt(t, project, mac, owner)
	work(t, mac.store, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", Text: "written first on the mac"})
	work(t, owner.store, ledger.WorkRequest{Action: ledger.WorkComment, ItemID: "widget-W1", SessionID: "lead", Text: "on the owner before the mac replayed"})
	syncOnce(t, project, mac, owner)
	sameLedger(t, project, mac, owner)
	item, _ := board(t, mac.store).WorkItem("widget-W1")
	if got := item.Timeline[len(item.Timeline)-1].Text; got != "written first on the mac" {
		t.Fatalf("mac timeline ends with %q, want the owner's order", got)
	}
}

func TestEveryStoredEventFitsOneReplay(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	adopt(t, project, mac, owner)
	whole := strings.Repeat("a\"é\n", ledger.MaxEventBytes/2)
	capture(t, mac.store, project, "claude-prompt:huge", whole)
	capture(t, mac.store, project, "claude-prompt:big", strings.Repeat("b", ledger.MaxEventBytes-1024))
	capture(t, mac.store, project, "claude-prompt:after", "and after it")
	capture(t, mac.store, project, "claude-prompt:huge", whole)
	syncOnce(t, project, mac, owner)
	sameLedger(t, project, mac, owner)

	sum := sha256.Sum256([]byte(whole))
	for _, u := range board(t, owner.store).Utterances() {
		if u.ID != "claude-prompt:huge" {
			continue
		}
		tr := u.Truncated
		if tr == nil || tr.OriginalBytes != len(whole) || tr.SHA256 != hex.EncodeToString(sum[:]) || !strings.HasPrefix(whole, strings.SplitN(u.Text, "\n[agenthail:", 2)[0]) || !strings.Contains(u.Text, "sha256 "+tr.SHA256) {
			t.Fatalf("the oversized words were not kept as a marked head: truncated=%+v, text ends %q", tr, u.Text[max(0, len(u.Text)-160):])
		}
		if !utf8.ValidString(u.Text) {
			t.Fatal("the kept head splits a character")
		}
		return
	}
	t.Fatal("the oversized words were not recorded")
}

func TestOwnershipNeedsOneSideWithoutHistory(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	searchAsk(t, owner.store)
	if err := owner.store.ClaimOwnership(project, mac.id, true); err == nil || !strings.Contains(err.Error(), "both hosts already hold ledger history") {
		t.Fatalf("claim with history on both sides: %v", err)
	}
	if err := mac.store.AssignOwner(project, mac.id); err == nil || !strings.Contains(err.Error(), "its own remote owner") {
		t.Fatalf("self as owner: %v", err)
	}
}

func TestTwoProjectsAdoptToDifferentOwners(t *testing.T) {
	const other = "repo:github.com/acme/infra"
	mac, forge, vps := newSyncHost(t), newSyncHost(t), newSyncHost(t)
	searchAsk(t, mac.store)
	capture(t, mac.store, other, "claude-prompt:infra", "rotate the keys")
	g, _, err := mac.store.EnsureGoal(other, "Keys", "s1")
	if err != nil {
		t.Fatal(err)
	}
	attach(t, mac.store, ledger.AttachRequest{ProjectKey: other, UtteranceID: "claude-prompt:infra", As: ledger.AsNewAsk, To: g.ID, Clauses: []string{"rotate the keys"}})

	adopt(t, project, mac, forge)
	adopt(t, other, mac, vps)
	sameLedger(t, project, mac, forge)
	sameLedger(t, other, mac, vps)
	for _, h := range []struct {
		name string
		host syncHost
		gone string
	}{{"forge", forge, other}, {"vps", vps, project}} {
		var n int
		if err := h.host.db.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=?`, h.gone).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s holds %d events of %s, which it does not own", h.name, n, h.gone)
		}
	}
	if err := mac.store.AssignOwner(project, vps.id); err == nil || !strings.Contains(err.Error(), "already syncs with") {
		t.Fatalf("moving an owned ledger: %v", err)
	}
}

func TestCaptureStateFollowsTheOwnersOrderOnBothHosts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(mac, owner syncHost) error
		want  int
	}{
		{"the owner disables, then the mac's enable replays", func(mac, owner syncHost) error {
			if err := owner.store.Disable(project); err != nil {
				return err
			}
			return mac.store.Enable(project)
		}, ledger.LedgerEnabled},
		{"the mac enables, then the owner disables after the replay", func(mac, owner syncHost) error {
			if err := mac.store.Enable(project); err != nil {
				return err
			}
			syncOnce(t, project, mac, owner)
			return owner.store.Disable(project)
		}, ledger.LedgerDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mac, owner := newSyncHost(t), newSyncHost(t)
			searchAsk(t, mac.store)
			adopt(t, project, mac, owner)
			for _, host := range []syncHost{mac, owner} {
				if _, err := host.store.Scope(project); err != nil {
					t.Fatal(err)
				}
			}
			if err := tc.write(mac, owner); err != nil {
				t.Fatal(err)
			}
			syncOnce(t, project, mac, owner)
			sameLedger(t, project, mac, owner)
			for name, host := range map[string]syncHost{"mac": mac, "owner": owner} {
				for _, store := range []*ledger.Store{host.store, ledger.NewStore(host.db, host.db)} {
					if scope, err := store.Scope(project); err != nil || scope.State != tc.want {
						t.Fatalf("%s scope = %+v err %v, want state %d", name, scope, err, tc.want)
					}
				}
			}
		})
	}
}
