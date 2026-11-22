package daemon

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/prwatch"
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/worktree"
)

func linkWatched(t *testing.T, r *registry.Registry, session string, number int) github.Ref {
	t.Helper()
	ref := github.Ref{Repo: acmeApp, Number: number}
	if _, err := r.LinkPR(registry.SessionPR{SessionID: session, Repo: acmeApp.String(), Number: number, URL: ref.URL(), Source: registry.PRSourceAgent}, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	return ref
}

func wakesFor(t *testing.T, r *registry.Registry, session string) []string {
	t.Helper()
	rows, err := r.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, row := range rows {
		if row.SessionID == session {
			out = append(out, row.Message)
		}
	}
	return out
}

func openStatus(ref github.Ref, head string, checks ...github.Check) github.Status {
	return github.Status{Ref: ref, Title: "Fix the login", State: "open", HeadSHA: head, HeadRef: "ah/x", Mergeable: "mergeable", Review: "none", Checks: checks}
}

func TestAWatchedPullRequestWakesItsSessionOncePerHead(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ref, other := linkWatched(t, r, "to", 42), linkWatched(t, r, "from", 43)
	fake := &fakeGitHub{viewer: "agent"}
	d.github = fake
	failing := github.Check{Name: "test", Required: true, State: github.CheckFailed}
	fake.setStatus(openStatus(ref, "aaaaaaa1111", failing))
	fake.setStatus(openStatus(other, "ccccccc3333"))
	now := time.Now()
	tick := func() {
		t.Helper()
		now = now.Add(prwatch.SweepEvery)
		if err := d.prs.Watch(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}

	tick()
	if len(fake.watchReads) != 1 || len(fake.watchReads[0]) != 2 {
		t.Fatalf("watch reads = %v, want one request for both pull requests", fake.watchReads)
	}
	wakes := wakesFor(t, r, "to")
	if len(wakes) != 1 || !strings.Contains(wakes[0], "checks failed on aaaaaaa: test") || !strings.Contains(wakes[0], ref.URL()) {
		t.Fatalf("wakes = %q", wakes)
	}
	tick()
	if wakes := wakesFor(t, r, "to"); len(wakes) != 1 {
		t.Fatalf("the same failure woke again: %q", wakes)
	}

	fake.setStatus(openStatus(ref, "bbbbbbb2222", failing))
	tick()
	if wakes := wakesFor(t, r, "to"); len(wakes) != 2 || !strings.Contains(wakes[1], "checks failed on bbbbbbb") {
		t.Fatalf("a new head did not re-arm: %q", wakes)
	}

	conflicted := openStatus(ref, "bbbbbbb2222", failing)
	conflicted.Mergeable = "conflicting"
	fake.setStatus(conflicted)
	tick()
	tick()
	if wakes := wakesFor(t, r, "to"); len(wakes) != 3 || !strings.Contains(wakes[2], "bbbbbbb conflicts with its base branch") {
		t.Fatalf("conflict wakes = %q", wakes)
	}
	if pr, _, _ := r.SessionPR("to", acmeApp.String(), 42); pr.Checks != "failing" || pr.Mergeable != "conflicting" || pr.HeadSHA != "bbbbbbb2222" {
		t.Fatalf("recorded %+v", pr)
	}

	if wakes := wakesFor(t, r, "from"); len(wakes) != 0 {
		t.Fatalf("a pull request with no checks woke: %q", wakes)
	}
	passed := openStatus(other, "ccccccc3333", github.Check{Name: "lint", State: github.CheckPassed})
	passed.Remarks = []github.Remark{{ID: "r1", Kind: github.RemarkReview, Author: "agent", State: "commented", At: now}}
	fake.setStatus(passed)
	tick()
	if wakes := wakesFor(t, r, "from"); len(wakes) != 1 || !strings.Contains(wakes[0], "all required checks passed on ccccccc") || strings.Contains(wakes[0], "reviewed") {
		t.Fatalf("passing wakes = %q", wakes)
	}
}

func TestASquashMergedPullRequestSettlesItsSessionsWorktreeWithoutAWakeAndRemovesItOnceArchived(t *testing.T) {
	checkout := pushedCheckout(t)
	d, r, _, _, _ := daemonFixture(t)
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"}); code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	path := filepath.Join(checkout, worktree.Dir, "ah-builder")
	if err := os.WriteFile(filepath.Join(path, "app", "feature.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, path, "add", ".")
	gitIn(t, path, "commit", "--quiet", "-m", "feature")
	gitIn(t, path, "push", "--quiet", "origin", "ah/builder")
	head := gitIn(t, path, "rev-parse", "HEAD")
	gitIn(t, checkout, "merge", "--quiet", "--squash", "ah/builder")
	gitIn(t, checkout, "commit", "--quiet", "-m", "squash")
	gitIn(t, checkout, "push", "--quiet", "origin", "HEAD:main")
	gitIn(t, checkout, "push", "--quiet", "origin", "--delete", "ah/builder")
	gitIn(t, checkout, "fetch", "--quiet", "--prune")
	gitIn(t, checkout, "remote", "add", "github", "https://github.com/acme/app.git")
	gitIn(t, checkout, "config", "branch.ah/builder.remote", "github")
	settleIdle(t, r, "started")
	ref := linkWatched(t, r, "started", 7)
	merged := openStatus(ref, head)
	merged.State, merged.HeadRef = "merged", "ah/builder"
	fake := &fakeGitHub{branches: map[github.Branch][]github.PullRequest{
		{Repo: acmeApp, Head: "ah/builder"}: {{Ref: ref, URL: ref.URL(), State: "merged", HeadSHA: head, HeadRef: "ah/builder", BaseRef: "main"}},
	}}
	fake.setStatus(merged)
	d.github = fake

	if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if work := workOf(t, r, "started"); work.State != registry.WorkSettled || work.SettledReason != registry.WorkSettledMerged {
		t.Fatalf("work %+v", work)
	}
	d.sweepWork(t.Context(), time.Now())
	if work := workOf(t, r, "started"); work.Cleanup != registry.WorkCleanupKept || work.CleanupReason != registry.WorkKeptOpen {
		t.Fatalf("the worktree of a session that is not archived: %+v", work)
	}
	if err := r.ArchiveSession("started", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.sweepWork(t.Context(), time.Now())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the squash-merged worktree of an archived session kept: %v %+v", err, workOf(t, r, "started"))
	}
	if len(fake.branchReads) == 0 || fake.unboundedBranchReads != 0 {
		t.Fatalf("cleanup read the branch's pull requests %d times, %d without a deadline", len(fake.branchReads), fake.unboundedBranchReads)
	}
	if wakes := wakesFor(t, r, "started"); len(wakes) != 0 {
		t.Fatalf("merging woke the session: %q", wakes)
	}
	if pr, _, _ := r.SessionPR("started", acmeApp.String(), 7); pr.Watch || pr.State != "merged" {
		t.Fatalf("merged row %+v", pr)
	}
}

func TestAMergedPullRequestFromAnotherBranchLeavesTheWorktree(t *testing.T) {
	checkout := pushedCheckout(t)
	d, r, _, _, _ := daemonFixture(t)
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"}); code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	ref := linkWatched(t, r, "started", 8)
	merged := openStatus(ref, "eeeeeee5555")
	merged.State, merged.HeadRef = "merged", "someone-else"
	fake := &fakeGitHub{}
	fake.setStatus(merged)
	d.github = fake
	if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if work := workOf(t, r, "started"); work.State != registry.WorkActive {
		t.Fatalf("another branch's merge settled the work: %+v", work)
	}
}

func TestAReportedTasksPullRequestWakesItsSpawner(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	if _, err := r.CreateTask("from", "to", "Fix the login", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SettleTask("to", registry.TaskSettlement{State: registry.TaskDone, Kind: registry.TaskKindReported, Result: "opened #42"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref := linkWatched(t, r, "to", 42)
	fake := &fakeGitHub{}
	fake.setStatus(openStatus(ref, "aaaaaaa1111", github.Check{Name: "test", State: github.CheckFailed}))
	d.github = fake
	if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if wakes := wakesFor(t, r, "to"); len(wakes) != 0 {
		t.Fatalf("a reported task was woken: %q", wakes)
	}
	if wakes := wakesFor(t, r, "from"); len(wakes) != 1 || !strings.Contains(wakes[0], "which already reported") || !strings.Contains(wakes[0], "checks failed") {
		t.Fatalf("spawner wakes = %q", wakes)
	}
}

func TestAHostGitHubRefusesShowsItsProblemAndBacksOff(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ref := linkWatched(t, r, "to", 42)
	fake := &fakeGitHub{watchErr: &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}}
	fake.setStatus(openStatus(ref, "aaaaaaa1111", github.Check{Name: "test", State: github.CheckFailed}))
	d.github = fake
	now := time.Now()
	if err := d.prs.Watch(t.Context(), now); err == nil {
		t.Fatal("a read without a gh sign-in reported success")
	}
	prs, _ := r.PRsForSession("to")
	if views := d.prs.Views(prs); len(views) != 1 || !strings.Contains(views[0].Problem, "gh auth login") {
		t.Fatalf("views = %+v", views)
	}
	if wakes := wakesFor(t, r, "to"); len(wakes) != 0 {
		t.Fatalf("a failed read woke: %q", wakes)
	}

	fake.watchErr = &github.RateLimitedError{Message: "API rate limit exceeded"}
	_ = d.prs.Watch(t.Context(), now)
	reads := len(fake.watchReads)
	_ = d.prs.Watch(t.Context(), now.Add(prwatch.SweepEvery))
	if len(fake.watchReads) != reads {
		t.Fatal("a rate-limited host was read again before its backoff")
	}
	fake.watchErr = nil
	if err := d.prs.Watch(t.Context(), now.Add(prwatch.BackoffFirst)); err != nil {
		t.Fatal(err)
	}
	prs, _ = r.PRsForSession("to")
	if views := d.prs.Views(prs); views[0].Problem != "" || views[0].Checks != "failing" {
		t.Fatalf("after the backoff = %+v", views)
	}
}

func TestAHostsGitHubProblemReachesASessionViewAlreadyBuilt(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ref := linkWatched(t, r, "to", 42)
	fake := &fakeGitHub{}
	fake.setStatus(openStatus(ref, "aaaaaaa1111"))
	d.github = fake
	if err := d.prs.Watch(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(t.Context())
	if prs := viewedPRs(t, d, "to"); len(prs) != 1 || prs[0].Problem != "" {
		t.Fatalf("before the refusal = %+v", prs)
	}
	fake.watchErr = &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}
	_ = d.prs.Watch(t.Context(), time.Now())
	d.discoverCatalog(t.Context())
	if prs := viewedPRs(t, d, "to"); len(prs) != 1 || !strings.Contains(prs[0].Problem, "gh auth login") {
		t.Fatalf("after the refusal = %+v", prs)
	}
}

func viewedPRs(t *testing.T, d *Daemon, id string) []prwatch.View {
	t.Helper()
	view, err := d.readSessionView(t.Context(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		if row.ID == id {
			return row.PRs
		}
	}
	t.Fatalf("%s is not in the session view", id)
	return nil
}
