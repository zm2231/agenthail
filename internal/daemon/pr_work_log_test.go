package daemon

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestAPullRequestIsLoggedOnlyToTheItemItIsFor(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := plainFolder(t)
	addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := d.ledgerStore()
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	worker := folderSession(t, r, "worker", path)
	solo := folderSession(t, r, "solo", path)
	work := func(req ledger.WorkRequest) ledger.WorkResult {
		t.Helper()
		req.ProjectKey = key
		result, err := store.Work(req, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	claim := func(session, text string) string {
		t.Helper()
		item := work(ledger.WorkRequest{SessionID: session, Action: ledger.WorkAdd, Text: text}).Item.ID
		work(ledger.WorkRequest{SessionID: session, Action: ledger.WorkClaim, ItemID: item})
		return item
	}
	login, test := claim("worker", "fix the login"), claim("worker", "add the test")
	only := claim("solo", "fix the logout")

	fixRef, soloRef := github.Ref{Repo: acmeApp, Number: 42}, github.Ref{Repo: acmeApp, Number: 43}
	fake := &fakeGitHub{prs: map[github.Ref]github.PullRequest{fixRef: acmePR(42, "Fix the login"), soloRef: acmePR(43, "Fix the logout")}}
	d.github = fake
	server := mcpServer(t, d)
	workerMCP, soloMCP := mcpSession(t, server, issueMCPToken(t, d, worker)), mcpSession(t, server, issueMCPToken(t, d, solo))
	link := func(session *mcp.ClientSession, args map[string]any) (item, from string) {
		t.Helper()
		out, refused := callMCP(t, session, "agenthail_pr", args)
		if refused {
			t.Fatalf("link %v: %v", args, out)
		}
		prs, _ := out["prs"].([]any)
		if len(prs) != 1 {
			t.Fatalf("link %v answered %v", args, out)
		}
		pr, _ := prs[0].(map[string]any)
		item, _ = pr["item"].(string)
		from, _ = pr["itemFrom"].(string)
		return item, from
	}

	logs := func(item string) []string {
		t.Helper()
		board, err := store.Board(key)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := board.WorkItem(item)
		var out []string
		for _, e := range w.Timeline {
			if e.Kind == "log" {
				if e.Actor != w.Assignee || e.LogType != "progress" || e.Source != ledger.WorkSourcePRItem {
					t.Errorf("%s log %+v is not the watcher's progress for its holder", item, e)
				}
				out = append(out, e.Text)
			}
		}
		return out
	}

	if item, from := link(workerMCP, map[string]any{"action": "link", "pr": fixRef.URL(), "clientRequestId": "l1"}); item != "" || from != "" {
		t.Fatalf("a link naming no item with two in progress is for %q (%q)", item, from)
	}
	if got := append(logs(login), logs(test)...); len(got) != 0 {
		t.Fatalf("a session with two items in progress linked a pull request naming neither; logged %q", got)
	}
	if code := refusalCode(t, workerMCP, "agenthail_pr", map[string]any{"action": "link", "pr": fixRef.URL(), "item": only, "clientRequestId": "l2"}); code != "invalid" {
		t.Fatalf("naming an item another session holds = %q", code)
	}
	if item, from := link(workerMCP, map[string]any{"action": "link", "pr": fixRef.URL(), "item": login, "clientRequestId": "l3"}); item != login || from != registry.PRItemNamed {
		t.Fatalf("a link naming %s is for %q (%q)", login, item, from)
	}
	if item, from := link(soloMCP, map[string]any{"action": "link", "pr": soloRef.URL(), "clientRequestId": "s1"}); item != only || from != registry.PRItemOnlyInProgress {
		t.Fatalf("a link from a session with one item in progress is for %q (%q)", item, from)
	}

	for _, ref := range []github.Ref{fixRef, soloRef} {
		merged := openStatus(ref, "abc123")
		merged.State = "merged"
		fake.setStatus(merged)
	}
	fake.duringRead = func() {
		fake.duringRead = nil
		if item, _ := link(workerMCP, map[string]any{"action": "link", "pr": fixRef.URL(), "item": test, "clientRequestId": "l4"}); item != test {
			t.Errorf("naming %s on the linked pull request left it for %q", test, item)
		}
	}
	for range 2 {
		if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for item, want := range map[string][]string{
		login: {"PR opened: " + fixRef.URL()},
		test:  {"PR opened: " + fixRef.URL(), "PR merged: " + fixRef.URL()},
		only:  {"PR opened: " + soloRef.URL(), "PR merged: " + soloRef.URL()},
	} {
		if got := logs(item); !slices.Equal(got, want) {
			t.Errorf("%s logs %q, want %q", item, got, want)
		}
	}

	gone := func(item string) (ledger.WorkResult, error) {
		return store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "solo", Action: ledger.WorkTakeover, ItemID: item, Reason: "its holder is gone",
			Takeover: &ledger.Takeover{Then: ledger.TakeoverDone, Holder: "worker", Verdict: ledger.HolderGone}}, time.Now())
	}
	if _, err := gone(login); err == nil || !strings.Contains(err.Error(), "PR merged") {
		t.Fatalf("finishing the item the pull request was moved off before it merged = %v", err)
	}
	if done, err := gone(test); err != nil || done.Item.State != ledger.WorkDone {
		t.Fatalf("finishing the item the merged pull request was for = %+v, %v", done.Item, err)
	}
}
