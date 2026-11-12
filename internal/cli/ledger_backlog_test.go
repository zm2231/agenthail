package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func ledgerBacklogFixture(t *testing.T, answer map[string]any) (*App, *[]url.Values) {
	t.Helper()
	app, _ := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, nil })
	var queries []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/ledger" {
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		queries = append(queries, r.URL.Query())
		json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, httpClient: server.Client()}, nil
	}
	return app, &queries
}

func TestLedgerBacklogAsksTheDaemonAndSaysWhyEachItemWaits(t *testing.T) {
	app, queries := ledgerBacklogFixture(t, map[string]any{"total": 2, "truncated": false, "backlog": []map[string]any{
		{"id": "widget-W3", "text": "long sweep", "kind": "usage", "ready": true, "why": "claude:bb has headroom"},
		{"id": "widget-W5", "text": "rename", "kind": "later", "ready": false, "why": "revisit 2026-12-01"},
	}})
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"backlog", "--kind", "usage, later", "--ready"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "widget-W3  ready  usage: claude:bb has headroom  long sweep") || !strings.Contains(out, "widget-W5  waiting  later: revisit 2026-12-01  rename") {
		t.Fatalf("output %q", out)
	}
	q := (*queries)[0]
	if q.Get("view") != "backlog" || q.Get("ready") != "true" || strings.Join(q["kind"], ",") != "usage,later" || q.Get("projectKey") != "repo:github.com/acme/widget" {
		t.Fatalf("query %v", q)
	}
	for _, args := range [][]string{
		{"backlog", "--kind", "soon"},
		{"backlog", "--triage", "--ready"},
		{"backlog", "--days", "3"},
		{"backlog", "extra"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	for _, args := range [][]string{
		{"work", "add", "x", "--days", "3"},
		{"work", "add", "x", "--ready"},
		{"goal", "x", "--triage"},
	} {
		if _, err := captureStdout(t, func() error { return app.cmdLedger(args) }); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("%v: err=%v, want the backlog flag refused", args, err)
		}
	}
	if len(*queries) != 1 {
		t.Fatalf("a refused backlog still asked the daemon: %v", *queries)
	}
}

func TestLedgerBacklogTriageListsIdleWorkAndHowToParkIt(t *testing.T) {
	app, queries := ledgerBacklogFixture(t, map[string]any{"total": 1, "idleDays": 30, "triage": []map[string]any{
		{"id": "widget-W9", "text": "old idea", "state": "open", "updatedAt": "2026-09-01T10:00:00Z"},
	}})
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"backlog", "--triage", "--days", "30"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "widget-W9  open  idle since 2026-09-0") || !strings.Contains(out, "agenthail ledger park <id>...") {
		t.Fatalf("output %q", out)
	}
	if q := (*queries)[0]; q.Get("view") != "triage" || q.Get("days") != "30" {
		t.Fatalf("query %v", q)
	}
}
