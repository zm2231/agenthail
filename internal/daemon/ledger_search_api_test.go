package daemon

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestLedgerSearchFindsWorkInEveryLedgerWithItsProject(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	addLedgerProject(t, r, "p-widget", "repo:github.com/acme/widget")
	addLedgerProject(t, r, "p-notes", "")
	notesKey, synced := registry.LedgerProjectKey("p-notes"), "repo:github.com/acme/laptop-only"
	store := d.ledgerStore()
	widget := addWork(t, store, "repo:github.com/acme/widget", "fix the widget parser")
	notes := addWork(t, store, notesKey, "parser notes")
	remote := addWork(t, store, synced, "parser on the laptop")
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: notesKey, SessionID: "worker", Action: ledger.WorkClose, ItemID: notes.ID, Reason: "done: written"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignOwner(synced, "h-laptop"); err != nil {
		t.Fatal(err)
	}

	type item struct {
		ProjectKey, ProjectName, Host, ID, State, Matched string
	}
	search := func(query string) ([]item, bool) {
		t.Helper()
		w := ledgerGet(t, handler, "/api/v1/ledger/search?"+query, "")
		if w.Code != http.StatusOK {
			t.Fatalf("search %s: %d %s", query, w.Code, w.Body)
		}
		var body struct {
			Items     []item `json:"items"`
			Truncated bool   `json:"truncated"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Items, body.Truncated
	}

	items, truncated := search("q=parser")
	byID := map[string]item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if len(items) != 3 || truncated {
		t.Fatalf("q=parser = %+v truncated=%v, want the three items", items, truncated)
	}
	if items[2].ID != notes.ID {
		t.Errorf("the done item ranks %+v, want it after the unfinished ones", items)
	}
	for id, want := range map[string]item{
		widget.ID: {ProjectKey: "repo:github.com/acme/widget", ProjectName: "p-widget", ID: widget.ID, State: "open", Matched: "title"},
		notes.ID:  {ProjectKey: notesKey, ProjectName: "p-notes", ID: notes.ID, State: "done", Matched: "title"},
		remote.ID: {ProjectKey: synced, ProjectName: synced, Host: "h-laptop", ID: remote.ID, State: "open", Matched: "title"},
	} {
		if byID[id] != want {
			t.Errorf("%s = %+v, want %+v", id, byID[id], want)
		}
	}

	for query, want := range map[string]string{
		"q=parser&project=p-notes": notes.ID,
		"q=parser&project=" + url.QueryEscape("repo:github.com/acme/widget"): widget.ID,
		"q=parser&state=done": notes.ID,
		"q=parser&state=open,blocked&project=" + url.QueryEscape(synced): remote.ID,
		"q=" + url.QueryEscape(widget.ID):                                widget.ID,
	} {
		items, _ := search(query)
		if len(items) != 1 || items[0].ID != want {
			t.Errorf("%s = %+v, want only %s", query, items, want)
		}
	}
	described, err := store.Work(ledger.WorkRequest{ProjectKey: "repo:github.com/acme/widget", SessionID: "worker", Action: ledger.WorkAdd, Text: "speed up startup",
		Fields: ledger.WorkFields{Description: "profiling shows the tokenizer dominates"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := ledgerGet(t, handler, "/api/v1/ledger/search?q=tokenizer", "")
	var wire struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wire); w.Code != http.StatusOK || err != nil || len(wire.Items) != 1 {
		t.Fatalf("q=tokenizer = %d %s (%v)", w.Code, w.Body, err)
	}
	got := wire.Items[0]
	updated, _ := got["updatedAt"].(string)
	if at, err := time.Parse(time.RFC3339Nano, updated); err != nil || at.IsZero() || got["id"] != described.Item.ID ||
		got["matched"] != "description" || got["snippet"] != "profiling shows the tokenizer dominates" || got["rank"] != nil {
		t.Errorf("a description hit on the wire = %v", got)
	}
	if items, truncated := search("q=parser&limit=1"); len(items) != 1 || !truncated {
		t.Errorf("limit=1 = %+v truncated=%v", items, truncated)
	}
	for _, query := range []string{"q=p", "q=", "q=parser&limit=many", "q=parser&limit=101", "q=parser&state=finished"} {
		if w := ledgerGet(t, handler, "/api/v1/ledger/search?"+query, ""); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_request") {
			t.Errorf("%s = %d %s, want a 400", query, w.Code, w.Body)
		}
	}
}
