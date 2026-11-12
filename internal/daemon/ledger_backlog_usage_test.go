package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAParkedItemReadsTheHeadroomOfTheProviderRootAndHostItNamed(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	full := []usageMeter{{Kind: "session", Percent: 97, ResetsAt: reset}, {Kind: "weekly", Percent: 40, ResetsAt: now.Add(72 * time.Hour)}}
	roomy := []usageMeter{{Kind: "session", Percent: 20, ResetsAt: reset}}
	sets := []usageSet{
		{Provider: "claude", Name: "default", Root: "/home/me/.claude", Primary: true, HostID: "here", Meters: full},
		{Provider: "claude", Name: "bb", Root: "/home/me/.claude-bb", HostID: "here", Meters: roomy},
		{Provider: "codex", HostID: "here", Meters: full},
		{Provider: "codex", HostID: "there", Meters: roomy},
		{Provider: "claude", Name: "broken", HostID: "here", Problem: "signed out"},
	}
	for _, tc := range []struct {
		target ledger.ParkUsageTarget
		want   ledger.Headroom
	}{
		{ledger.ParkUsageTarget{Provider: "claude"}, ledger.Headroom{Known: true, Frees: reset}},
		{ledger.ParkUsageTarget{Provider: "claude", Root: "bb"}, ledger.Headroom{Known: true, Room: true}},
		{ledger.ParkUsageTarget{Provider: "claude", Root: "/home/me/.claude-bb"}, ledger.Headroom{Known: true, Room: true}},
		{ledger.ParkUsageTarget{Provider: "codex", HostID: "here"}, ledger.Headroom{Known: true, Frees: reset}},
		{ledger.ParkUsageTarget{Provider: "codex", HostID: "there"}, ledger.Headroom{Known: true, Room: true}},
		{ledger.ParkUsageTarget{Provider: "claude", Root: "broken"}, ledger.Headroom{}},
		{ledger.ParkUsageTarget{Provider: "claude", Root: "missing"}, ledger.Headroom{}},
	} {
		if got := parkHeadroom(sets, tc.target, now); got != tc.want {
			t.Errorf("%+v = %+v, want %+v", tc.target, got, tc.want)
		}
	}
}

func TestTheLedgerReadRouteServesTheBacklogAndTriage(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-backlog", "backlog-key")
	p.session(t, r, "worker")
	store := d.ledgerStore()
	if err := store.Enable(p.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	reset := addWork(t, store, p.key, "after the reset")
	waiting := addWork(t, store, p.key, "some day")
	addWork(t, store, p.key, "nobody holds this")
	for id, park := range map[string]ledger.Park{
		reset.ID:   {Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex", ResetsAt: time.Now().Add(-time.Minute)}},
		waiting.ID: {Kind: ledger.ParkLater, Why: "not now"},
	} {
		if _, err := store.Work(ledger.WorkRequest{ProjectKey: p.key, SessionID: "worker", Action: ledger.WorkPark, ItemID: id, Park: &park}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	body, etag := ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=backlog")
	rows := body["backlog"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"] != reset.ID || rows[0].(map[string]any)["ready"] != true || rows[1].(map[string]any)["ready"] != false || etag == "" {
		t.Fatalf("backlog = %v", body)
	}
	body, _ = ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=backlog&ready=true&kind=later")
	if rows := body["backlog"].([]any); len(rows) != 0 {
		t.Fatalf("ready later work = %v", body)
	}
	body, _ = ledgerGetJSON(t, handler, "/api/v1/ledger?projectKey="+p.key+"&view=triage&days=1")
	if rows := body["triage"].([]any); len(rows) != 0 || body["idleDays"] != float64(1) {
		t.Fatalf("triage of fresh work = %v", body)
	}
	for _, query := range []string{"&view=backlog&ready=maybe", "&view=triage&days=0", "&view=backlog&kind=soon"} {
		if w := ledgerGet(t, handler, "/api/v1/ledger?projectKey="+p.key+query, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", query, w.Code, w.Body)
		}
	}
}
