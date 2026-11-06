package daemon

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		now = now.Add(prSweepEvery)
		if err := d.watchPRs(t.Context(), now); err != nil {
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

func TestAMergedPullRequestSettlesItsSessionsWorktreeWithoutAWake(t *testing.T) {
	checkout := pushedCheckout(t)
	d, r, _, _, _ := daemonFixture(t)
	if code, answer := workAction(t, d, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": checkout, "isolation": "worktree", "alias": "builder"}); code != http.StatusCreated {
		t.Fatal(code, answer)
	}
	path := filepath.Join(checkout, worktree.Dir, "ah-builder")
	gitIn(t, path, "push", "--quiet", "origin", "ah/builder")
	settleIdle(t, r, "started")
	ref := linkWatched(t, r, "started", 7)
	merged := openStatus(ref, "ddddddd4444")
	merged.State, merged.HeadRef = "merged", "ah/builder"
	fake := &fakeGitHub{}
	fake.setStatus(merged)
	d.github = fake

	if err := d.watchPRs(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if work := workOf(t, r, "started"); work.State != registry.WorkSettled || work.SettledReason != registry.WorkSettledMerged {
		t.Fatalf("work %+v", work)
	}
	d.sweepWork(t.Context(), time.Now())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree of a merged pull request kept: %v", err)
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
	if err := d.watchPRs(t.Context(), time.Now()); err != nil {
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
	if err := d.watchPRs(t.Context(), time.Now()); err != nil {
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
	if err := d.watchPRs(t.Context(), now); err == nil {
		t.Fatal("a read without a gh sign-in reported success")
	}
	prs, _ := r.PRsForSession("to")
	if views := d.prViews(prs); len(views) != 1 || !strings.Contains(views[0].Problem, "gh auth login") {
		t.Fatalf("views = %+v", views)
	}
	if wakes := wakesFor(t, r, "to"); len(wakes) != 0 {
		t.Fatalf("a failed read woke: %q", wakes)
	}

	fake.watchErr = &github.RateLimitedError{Message: "API rate limit exceeded"}
	_ = d.watchPRs(t.Context(), now)
	reads := len(fake.watchReads)
	_ = d.watchPRs(t.Context(), now.Add(prSweepEvery))
	if len(fake.watchReads) != reads {
		t.Fatal("a rate-limited host was read again before its backoff")
	}
	fake.watchErr = nil
	if err := d.watchPRs(t.Context(), now.Add(prBackoffFirst)); err != nil {
		t.Fatal(err)
	}
	prs, _ = r.PRsForSession("to")
	if views := d.prViews(prs); views[0].Problem != "" || views[0].Checks != "failing" {
		t.Fatalf("after the backoff = %+v", views)
	}
}

func TestAHostsGitHubProblemReachesASessionViewAlreadyBuilt(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	ref := linkWatched(t, r, "to", 42)
	fake := &fakeGitHub{}
	fake.setStatus(openStatus(ref, "aaaaaaa1111"))
	d.github = fake
	if err := d.watchPRs(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	d.discoverCatalog(t.Context())
	if prs := viewedPRs(t, d, "to"); len(prs) != 1 || prs[0].Problem != "" {
		t.Fatalf("before the refusal = %+v", prs)
	}
	fake.watchErr = &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}
	_ = d.watchPRs(t.Context(), time.Now())
	d.discoverCatalog(t.Context())
	if prs := viewedPRs(t, d, "to"); len(prs) != 1 || !strings.Contains(prs[0].Problem, "gh auth login") {
		t.Fatalf("after the refusal = %+v", prs)
	}
}

func viewedPRs(t *testing.T, d *Daemon, id string) []prView {
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
