package ledger_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const gadget = "repo:github.com/acme/gadget"

func workAt(t *testing.T, s *ledger.Store, at time.Time, req ledger.WorkRequest) string {
	t.Helper()
	if req.ProjectKey == "" {
		req.ProjectKey = project
	}
	req.SessionID = "s1"
	result, err := s.Work(req, at)
	if err != nil {
		t.Fatalf("work %+v: %v", req, err)
	}
	return result.Item.ID
}

func searched(t *testing.T, s *ledger.Store, req ledger.WorkSearch) (string, ledger.WorkSearchResult) {
	t.Helper()
	result, err := s.SearchWork(req)
	if err != nil {
		t.Fatalf("search %+v: %v", req, err)
	}
	hits := make([]string, len(result.Items))
	for i, h := range result.Items {
		hits[i] = h.ID + ":" + h.Matched
	}
	return strings.Join(hits, " "), result
}

func TestSearchWorkRanksAcrossLedgers(t *testing.T) {
	s := newStore(t)
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }
	crash := workAt(t, s, at(0), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "parser crash on empty input"})
	docs := workAt(t, s, at(1), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "fix the docs",
		Fields: ledger.WorkFields{Description: strings.Repeat("background words ", 12) + "the Parser is slow on large files " + strings.Repeat("trailing words ", 12)}})
	rewrite := workAt(t, s, at(2), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "parser rewrite"})
	workAt(t, s, at(3), ledger.WorkRequest{Action: ledger.WorkClose, ItemID: rewrite, Reason: "done: shipped"})
	noted := workAt(t, s, at(4), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "speed up startup"})
	workAt(t, s, at(5), ledger.WorkRequest{Action: ledger.WorkComment, ItemID: noted, Text: "the parser warms first"})
	gone := workAt(t, s, at(6), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "parser idea"})
	workAt(t, s, at(6), ledger.WorkRequest{Action: ledger.WorkDelete, ItemID: gone})
	timeout := workAt(t, s, at(7), ledger.WorkRequest{ProjectKey: gadget, Action: ledger.WorkAdd, Text: "parser timeout"})
	follow := workAt(t, s, at(8), ledger.WorkRequest{ProjectKey: gadget, Action: ledger.WorkAdd, Text: "follow up on " + docs})
	imported, err := s.ImportWork(ledger.WorkImportRequest{ProjectKey: project, SessionID: "s1", Items: []ledger.WorkImport{
		{ImportedFrom: "td:td-50b9b1", Text: "carried over from td", CreatedAt: at(9)}}}, at(9))
	if err != nil || len(imported.Imported) != 1 {
		t.Fatalf("import = %+v, %v", imported, err)
	}
	carried := imported.Imported[0].ItemID
	port := workAt(t, s, at(10), ledger.WorkRequest{ProjectKey: gadget, Action: ledger.WorkAdd, Text: "port td-50b9b1 to the ledger"})

	got, result := searched(t, s, ledger.WorkSearch{Text: "PARSER"})
	want := strings.Join([]string{timeout + ":title", crash + ":title", rewrite + ":title", noted + ":notes", docs + ":description"}, " ")
	if got != want {
		t.Fatalf("parser:\n got %s\nwant %s", got, want)
	}
	if result.Truncated {
		t.Fatal("five hits under the default limit read as truncated")
	}
	for _, h := range result.Items {
		switch h.ID {
		case docs:
			if h.ProjectKey != project || !strings.Contains(h.Snippet, "the Parser is slow") || !strings.HasPrefix(h.Snippet, "…") ||
				!strings.HasSuffix(h.Snippet, "…") || len([]rune(h.Snippet)) > 122 {
				t.Fatalf("description hit = %+v, want a cut excerpt around the match", h)
			}
		case timeout:
			if h.ProjectKey != gadget || h.Snippet != "" {
				t.Fatalf("title hit = %+v, want gadget's with no snippet", h)
			}
		}
	}

	for _, c := range []struct {
		name string
		req  ledger.WorkSearch
		want string
	}{
		{"an exact id outranks a title naming it", ledger.WorkSearch{Text: strings.ToLower(docs)}, docs + ":id " + follow + ":title"},
		{"an exact td id ranks first", ledger.WorkSearch{Text: "td-50b9b1"}, carried + ":tdId " + port + ":title"},
		{"every word must match", ledger.WorkSearch{Text: "parser warms"}, noted + ":notes"},
		{"a state filter", ledger.WorkSearch{Text: "parser", States: []ledger.WorkState{ledger.WorkDone}}, rewrite + ":title"},
		{"deleted only when asked", ledger.WorkSearch{Text: "parser", States: []ledger.WorkState{ledger.WorkDeleted}}, gone + ":title"},
		{"a project filter", ledger.WorkSearch{Text: "parser", Projects: []string{gadget}}, timeout + ":title"},
	} {
		if got, _ := searched(t, s, c.req); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	if got, result := searched(t, s, ledger.WorkSearch{Text: "parser", Limit: 2}); got != timeout+":title "+crash+":title" || !result.Truncated {
		t.Fatalf("limit 2 = %q truncated=%v", got, result.Truncated)
	}
	for _, req := range []ledger.WorkSearch{{Text: "p"}, {Text: "  "}, {Text: "parser", Limit: ledger.MaxSearchLimit + 1},
		{Text: "parser", States: []ledger.WorkState{"finished"}}} {
		var refusal *ledger.Refusal
		if _, err := s.SearchWork(req); !errors.As(err, &refusal) {
			t.Errorf("search %+v: err = %v, want a refusal", req, err)
		}
	}
}

func TestSearchWorkReadsALedgerPulledFromItsOwner(t *testing.T) {
	mac, owner := newSyncHost(t), newSyncHost(t)
	adopt(t, project, mac, owner)
	shipped := workAt(t, owner.store, time.Now(), ledger.WorkRequest{Action: ledger.WorkAdd, Text: "ship the release notes"})
	workAt(t, owner.store, time.Now(), ledger.WorkRequest{Action: ledger.WorkClose, ItemID: shipped, Reason: "done: published"})
	syncOnce(t, project, mac, owner)

	if got, _ := searched(t, mac.store, ledger.WorkSearch{Text: "release notes"}); got != shipped+":title" {
		t.Fatalf("search on the follower = %q, want the owner's done item %s", got, shipped)
	}
	if remote, err := mac.store.RemoteOwner(project); err != nil || remote != owner.id {
		t.Fatalf("RemoteOwner = %q, %v; want %s", remote, err, owner.id)
	}
}
