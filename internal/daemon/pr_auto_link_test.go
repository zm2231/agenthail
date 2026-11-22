package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/worktree/worktreetest"
)

func TestWorktreeBranchesLinkTheirPullRequestsInOneRequest(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	checkout := worktreetest.PushedCheckout(t)
	worktreetest.Git(t, checkout, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	begun := time.Now().Add(-time.Hour)
	for session, branch := range map[string]string{"to": "ah/x", "from": "ah/y"} {
		path := checkout + "/.worktrees/" + branch
		if err := r.BeginWork(registry.WorkRecord{WorktreePath: path, RepoRoot: checkout, Branch: branch, BaseRef: "main", BaseCommit: "abc"}, begun); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ActivateWork(path, session); err != nil {
			t.Fatal(err)
		}
	}
	opened, stale := acmePR(7, "Branch work"), acmePR(3, "Old work on a reused name")
	opened.CreatedAt, stale.CreatedAt = begun.Add(time.Minute), begun.Add(-time.Hour)
	fake := &fakeGitHub{branches: map[github.Branch][]github.PullRequest{{Repo: acmeApp, Head: "ah/x"}: {opened, stale}}}
	d.github = fake

	if err := d.prs.LinkBranches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fake.branchReads) != 1 || len(fake.branchReads[0]) != 2 {
		t.Fatalf("branch reads = %v, want one request for both branches", fake.branchReads)
	}
	prs, err := r.PRsForSession("to")
	if err != nil || len(prs) != 1 || prs[0].Number != 7 || prs[0].Source != registry.PRSourceBranch || prs[0].Title != "Branch work" {
		t.Fatalf("linked = %+v, %v; want only the pull request opened after the worktree", prs, err)
	}

	if _, err := r.UnlinkPR("to", "github.com/acme/app", 7); err != nil {
		t.Fatal(err)
	}
	if err := d.prs.LinkBranches(t.Context()); err != nil {
		t.Fatal(err)
	}
	if prs, _ := r.PRsForSession("to"); len(prs) != 0 {
		t.Fatalf("an unlinked pull request came back: %+v", prs)
	}
	if last := fake.branchReads[len(fake.branchReads)-1]; len(last) != 1 || last[0].Head != "ah/y" {
		t.Fatalf("second sweep read %v, want only the branch with no pull request", last)
	}

	fake.err = &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}
	if err := d.prs.LinkBranches(t.Context()); err == nil {
		t.Fatal("a sweep without a gh sign-in reported success")
	}
}

func TestAClippedTimelineItemReadsItsFullBodyFromTheJournal(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	full := strings.Repeat("remote: building\n", 20000) + "https://github.com/acme/app/pull/42\n"
	entry := registry.SessionJournalEntry{SessionID: "to", Kind: "toolResult", ProviderKey: "result-body", Payload: []byte(`{}`), BodyRef: "result-body", FullBody: []byte(full), ObservedAt: time.Now()}
	if _, _, err := r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: 100, Bytes: 8 << 20}); err != nil {
		t.Fatal(err)
	}
	host := prHost{d}
	if got := host.TimelineText("to", surface.TimelineItem{Text: full[:16<<10], Truncated: true, BodyRef: "result-body"}); got != full {
		t.Fatalf("clipped item read %d bytes, want the full %d", len(got), len(full))
	}
	if got := host.TimelineText("to", surface.TimelineItem{Text: "whole", Truncated: true, BodyRef: "missing"}); got != "whole" {
		t.Fatalf("an item whose body is gone reads %q, want its own text", got)
	}
}
