package daemon

import (
	"encoding/json"
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

func TestAUsageParkRecordsTheHostRootAndResetFromTheParkingHostsReading(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	reset := now.Add(90 * time.Minute)
	sets := []usageSet{
		{Provider: "claude", Name: "default", Root: "/home/me/.claude", Primary: true, HostID: "here", Meters: []usageMeter{{Kind: "session", Percent: 95, ResetsAt: reset}}},
		{Provider: "codex", HostID: "here", Meters: []usageMeter{{Kind: "session", Percent: 10, ResetsAt: reset}}},
	}
	reads := 0
	local := func() []usageSet { reads++; return sets }
	park := func(target ledger.ParkUsageTarget) ledger.ParkUsageTarget {
		t.Helper()
		body, _ := json.Marshal(ledger.WorkRequest{ProjectKey: "k", SessionID: "worker", Action: ledger.WorkPark, ItemID: "widget-W1", Reason: "kept",
			Park: &ledger.Park{Kind: ledger.ParkUsage, Why: "out of usage", Usage: &target}})
		var req ledger.WorkRequest
		if err := json.Unmarshal(stampUsageParkFrom(body, "here", local, now), &req); err != nil {
			t.Fatal(err)
		}
		if req.Reason != "kept" || req.Park.Why != "out of usage" || req.ItemID != "widget-W1" {
			t.Fatalf("the stamp lost the rest of the request: %+v", req)
		}
		return *req.Park.Usage
	}
	if got := park(ledger.ParkUsageTarget{Provider: "claude"}); got.HostID != "here" || got.Root != "default" || !got.ResetsAt.Equal(reset) {
		t.Fatalf("claude at its limit = %+v", got)
	}
	if got := park(ledger.ParkUsageTarget{Provider: "codex"}); got.HostID != "here" || !got.ResetsAt.IsZero() {
		t.Fatalf("codex with headroom = %+v", got)
	}
	given := now.Add(time.Hour)
	if got := park(ledger.ParkUsageTarget{Provider: "claude", ResetsAt: given}); !got.ResetsAt.Equal(given) {
		t.Fatalf("a reset the caller gave = %+v", got)
	}
	before := reads
	if got := park(ledger.ParkUsageTarget{Provider: "claude", HostID: "there"}); got.HostID != "there" || !got.ResetsAt.IsZero() || reads != before {
		t.Fatalf("a park for another host = %+v, %d reads", got, reads-before)
	}
	for _, body := range []string{`{"action":"claim","itemId":"widget-W1"}`, `{"action":"park","itemId":"widget-W1","park":{"kind":"later","why":"x"}}`, `not json`} {
		if got := string(stampUsageParkFrom([]byte(body), "here", local, now)); got != body {
			t.Fatalf("%s became %s", body, got)
		}
	}
}

func TestAUsageItemWithNoLiveReadingComesBackAfterItsRecordedReset(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-reset", "reset-key")
	p.session(t, r, "worker")
	store := d.ledgerStore()
	if err := store.Enable(p.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	item := addWork(t, store, p.key, "long sweep")
	now := time.Now().Truncate(time.Second)
	reset := now.Add(time.Hour)
	sets := []usageSet{{Provider: "claude", Name: "default", Primary: true, HostID: "here", Meters: []usageMeter{{Kind: "weekly", Percent: 99, ResetsAt: reset}}}}
	body, _ := json.Marshal(ledger.WorkRequest{ProjectKey: p.key, SessionID: "worker", Action: ledger.WorkPark, ItemID: item.ID, Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "claude"}}})
	var req ledger.WorkRequest
	if err := json.Unmarshal(stampUsageParkFrom(body, "here", func() []usageSet { return sets }, now), &req); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Work(req, now); err != nil {
		t.Fatal(err)
	}
	unread := func(ledger.ParkUsageTarget) ledger.Headroom { return ledger.Headroom{} }
	for at, ready := range map[time.Time]bool{now.Add(time.Minute): false, reset: true, reset.Add(time.Hour): true} {
		result, err := store.Read(p.key, ledger.ReadRequest{View: "backlog", Now: at, Usage: unread})
		if err != nil {
			t.Fatal(err)
		}
		if rows := result["backlog"].([]map[string]any); len(rows) != 1 || rows[0]["ready"] != ready {
			t.Fatalf("at %s with no live reading: %+v, want ready=%v", at, rows, ready)
		}
	}
}

func TestTheWorkRouteRecordsTheParkingHostOnAUsagePark(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-stamp", "stamp-key")
	p.session(t, r, "worker")
	store := d.ledgerStore()
	if err := store.Enable(p.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	item := addWork(t, store, p.key, "long sweep")
	park := ledger.WorkRequest{ProjectKey: p.key, SessionID: "worker", Action: ledger.WorkPark, ItemID: item.ID, Park: &ledger.Park{Kind: ledger.ParkUsage, Usage: &ledger.ParkUsageTarget{Provider: "codex"}}}
	var result ledger.WorkResult
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", park, &result); code != http.StatusOK {
		t.Fatalf("park status=%d body=%s", code, body)
	}
	if got := result.Item.Park.Usage; got.HostID == "" || got.HostID != d.localHost().ID || got.Provider != "codex" {
		t.Fatalf("parked usage = %+v, want this host %q", got, d.localHost().ID)
	}
}
