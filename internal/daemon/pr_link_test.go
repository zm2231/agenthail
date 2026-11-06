package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

type fakeGitHub struct {
	mu          sync.Mutex
	prs         map[github.Ref]github.PullRequest
	branches    map[github.Branch][]github.PullRequest
	err         error
	branchReads [][]github.Branch
	statuses    map[github.Ref]github.Status
	viewer      string
	watchErr    error
	watchReads  [][]github.Ref
}

func (f *fakeGitHub) Watch(_ context.Context, host string, refs []github.Ref) (github.Watch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watchReads = append(f.watchReads, refs)
	if f.watchErr != nil {
		return github.Watch{}, f.watchErr
	}
	watch := github.Watch{Viewer: f.viewer, Statuses: map[github.Ref]github.Status{}, RateRemaining: 5000}
	for _, ref := range refs {
		if status, ok := f.statuses[ref]; ok && ref.Host == host {
			watch.Statuses[ref] = status
		}
	}
	return watch, nil
}

func (f *fakeGitHub) setStatus(status github.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses == nil {
		f.statuses = map[github.Ref]github.Status{}
	}
	f.statuses[status.Ref] = status
}

func (f *fakeGitHub) PullRequest(_ context.Context, ref github.Ref) (github.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return github.PullRequest{}, f.err
	}
	pr, ok := f.prs[ref]
	if !ok {
		return github.PullRequest{}, github.ErrNotFound
	}
	return pr, nil
}

func (f *fakeGitHub) BranchPullRequests(_ context.Context, branches []github.Branch) (map[github.Branch][]github.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchReads = append(f.branchReads, branches)
	if f.err != nil {
		return nil, f.err
	}
	found := map[github.Branch][]github.PullRequest{}
	for _, branch := range branches {
		if prs := f.branches[branch]; len(prs) > 0 {
			found[branch] = prs
		}
	}
	return found, nil
}

var acmeApp = github.Repo{Host: "github.com", Owner: "acme", Name: "app"}

func acmePR(number int, title string) github.PullRequest {
	ref := github.Ref{Repo: acmeApp, Number: number}
	return github.PullRequest{Ref: ref, URL: ref.URL(), Title: title, State: "open", HeadSHA: "abc123"}
}

func TestAnAgentLinksUnlinksAndWatchesPullRequests(t *testing.T) {
	f := newMCPWriteFixture(t)
	fake := &fakeGitHub{prs: map[github.Ref]github.PullRequest{{Repo: acmeApp, Number: 42}: acmePR(42, "Fix the login")}}
	f.d.github = fake
	url := "https://github.com/acme/app/pull/42"

	out, refused := callMCP(t, f.parent, "agenthail_pr", map[string]any{"action": "link", "pr": url, "session": "to", "clientRequestId": "l1"})
	if refused {
		t.Fatalf("spawner linking its child's pull request: %v", out)
	}
	pr, found, err := f.r.SessionPR("to", "github.com/acme/app", 42)
	if err != nil || !found || pr.Source != "agent" || pr.Title != "Fix the login" || pr.State != "open" || !pr.Watch {
		t.Fatalf("linked row = %+v, %v, %v", pr, found, err)
	}
	if code := refusalCode(t, f.child, "agenthail_pr", map[string]any{"action": "link", "pr": url, "session": "from", "clientRequestId": "l2"}); code != "out_of_scope" {
		t.Fatalf("child linking on its spawner: code=%q", code)
	}
	if code := refusalCode(t, f.child, "agenthail_pr", map[string]any{"action": "link", "pr": "https://github.com/acme/app/pull/43", "clientRequestId": "l3"}); code != "not_found" {
		t.Fatalf("linking a pull request GitHub does not have: code=%q", code)
	}

	if out, refused := callMCP(t, f.child, "agenthail_pr", map[string]any{"action": "unwatch", "pr": "42", "clientRequestId": "w1"}); refused {
		t.Fatalf("unwatching by number: %v", out)
	}
	if pr, _, _ := f.r.SessionPR("to", "github.com/acme/app", 42); pr.Watch {
		t.Fatal("unwatch left the watch on")
	}
	if out, refused := callMCP(t, f.child, "agenthail_pr", map[string]any{"action": "unlink", "pr": url, "clientRequestId": "u1"}); refused {
		t.Fatalf("unlinking: %v", out)
	}
	if prs, err := f.r.PRsForSession("to"); err != nil || len(prs) != 0 {
		t.Fatalf("after unlink = %+v, %v", prs, err)
	}
	if code := refusalCode(t, f.child, "agenthail_pr", map[string]any{"action": "watch", "pr": url, "clientRequestId": "w2"}); code != "not_found" {
		t.Fatalf("watching an unlinked pull request: code=%q", code)
	}

	fake.err = &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}
	out, refused = callMCP(t, f.child, "agenthail_pr", map[string]any{"action": "link", "pr": url, "clientRequestId": "l4"})
	message, _ := out["message"].(string)
	if !refused || !strings.Contains(message, "gh auth login") {
		t.Fatalf("linking without a gh sign-in: refused=%v %v", refused, out)
	}
	if prs, _ := f.r.PRsForSession("to"); len(prs) != 0 {
		t.Fatalf("a refused link recorded %+v", prs)
	}
}

func TestAPullRequestLinkedOutsideTheDaemonShowsInTheNextSessionView(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	viewed := func() []prView {
		t.Helper()
		view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range view.Sessions {
			if row.ID == "from" {
				return row.PRs
			}
		}
		t.Fatal("from is not in the session view")
		return nil
	}
	d.discoverCatalog(context.Background())
	if prs := viewed(); len(prs) != 0 {
		t.Fatalf("before linking: %+v", prs)
	}
	if _, err := reg.LinkPR(registry.SessionPR{SessionID: "from", Repo: "github.com/acme/app", Number: 7, URL: "https://github.com/acme/app/pull/7", Source: registry.PRSourceBranch, Title: "Seven"}, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if prs := viewed(); len(prs) != 1 || prs[0].Number != 7 || prs[0].Title != "Seven" || !prs[0].Watch {
		t.Fatalf("after linking: %+v", prs)
	}
	if _, err := reg.UnlinkPR("from", "github.com/acme/app", 7); err != nil {
		t.Fatal(err)
	}
	if prs := viewed(); len(prs) != 0 {
		t.Fatalf("after unlinking: %+v", prs)
	}
}

func TestAPullRequestLinkedWatchedOrUnlinkedRepublishesItsSessionsRow(t *testing.T) {
	f := newMCPWriteFixture(t)
	f.d.github = &fakeGitHub{prs: map[github.Ref]github.PullRequest{{Repo: acmeApp, Number: 42}: acmePR(42, "Fix the login")}}
	for _, action := range []string{"link", "unwatch", "unlink"} {
		select {
		case <-f.d.catalogNudge:
		default:
		}
		if out, refused := callMCP(t, f.child, "agenthail_pr", map[string]any{"action": action, "pr": "https://github.com/acme/app/pull/42", "clientRequestId": action}); refused {
			t.Fatalf("%s: %v", action, out)
		}
		select {
		case <-f.d.catalogNudge:
		default:
			t.Fatalf("%s did not republish the session's row", action)
		}
	}
}
