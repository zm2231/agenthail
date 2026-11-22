package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAPullRequestsLifeIsLoggedToEveryItemItsSessionHolds(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := plainFolder(t)
	addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := d.ledgerStore()
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	worker := folderSession(t, r, "worker", path)
	folderSession(t, r, "other", path)
	work := func(req ledger.WorkRequest) ledger.WorkResult {
		t.Helper()
		req.ProjectKey = key
		result, err := store.Work(req, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	var held []string
	for _, text := range []string{"fix the login", "add the test"} {
		item := work(ledger.WorkRequest{SessionID: "worker", Action: ledger.WorkAdd, Text: text}).Item.ID
		work(ledger.WorkRequest{SessionID: "worker", Action: ledger.WorkClaim, ItemID: item})
		held = append(held, item)
	}
	elsewhere := work(ledger.WorkRequest{SessionID: "other", Action: ledger.WorkAdd, Text: "unrelated"}).Item.ID
	work(ledger.WorkRequest{SessionID: "other", Action: ledger.WorkClaim, ItemID: elsewhere})

	ref := github.Ref{Repo: acmeApp, Number: 42}
	fake := &fakeGitHub{prs: map[github.Ref]github.PullRequest{ref: acmePR(42, "Fix the login")}}
	d.github = fake
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, worker))
	for _, id := range []string{"l1", "l2"} {
		if out, refused := callMCP(t, session, "agenthail_pr", map[string]any{"action": "link", "pr": ref.URL(), "clientRequestId": id}); refused {
			t.Fatalf("link: %v", out)
		}
	}
	merged := openStatus(ref, "abc123")
	merged.State = "merged"
	fake.setStatus(merged)
	if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}

	logs := func(item string) []string {
		t.Helper()
		board, err := store.Board(key)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, w := range board.WorkItems() {
			if w.ID != item {
				continue
			}
			for _, e := range w.Timeline {
				if e.Kind == "log" {
					if e.Actor != w.Assignee || e.LogType != "progress" {
						t.Errorf("%s log %+v is not the holder's progress", item, e)
					}
					out = append(out, e.Text)
				}
			}
		}
		return out
	}
	want := []string{"PR opened: " + ref.URL(), "PR merged: " + ref.URL()}
	for _, item := range held {
		if got := logs(item); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s logs %q, want %q", item, got, want)
		}
	}
	if got := logs(elsewhere); len(got) != 0 {
		t.Errorf("another session's item got %q", got)
	}
}
