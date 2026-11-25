package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/worktree"
)

func TestTheSweepRemovesHandMadeWorktreesOnlyOnceTheirPullRequestMergedAndNoOpenSessionWorksThere(t *testing.T) {
	checkout := pushedCheckout(t)
	d, r, _, _, _ := daemonFixture(t)
	addFolderProject(t, d, checkout)
	gitIn(t, checkout, "remote", "add", "github", "https://github.com/acme/app.git")
	fake := &fakeGitHub{branches: map[github.Branch][]github.PullRequest{}}
	d.github = fake

	handMade := func(branch string) (path, head string) {
		t.Helper()
		path = filepath.Join(checkout, worktree.Dir, worktree.Slug(branch))
		gitIn(t, checkout, "worktree", "add", "--quiet", "-b", branch, path)
		gitIn(t, checkout, "config", "branch."+branch+".remote", "github")
		if err := os.WriteFile(filepath.Join(path, "app", worktree.Slug(branch)+".go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, path, "add", ".")
		gitIn(t, path, "commit", "--quiet", "-m", branch)
		return path, gitIn(t, path, "rev-parse", "HEAD")
	}
	mergedInto := func(base, branch, head string, number int) {
		t.Helper()
		fake.branches[github.Branch{Repo: acmeApp, Head: branch}] = []github.PullRequest{{Ref: github.Ref{Repo: acmeApp, Number: number}, URL: github.Ref{Repo: acmeApp, Number: number}.URL(), State: "merged", HeadSHA: head, HeadRef: branch, BaseRef: base}}
	}
	squashMerged := func(branch, head string, number int) {
		t.Helper()
		gitIn(t, checkout, "merge", "--quiet", "--squash", branch)
		gitIn(t, checkout, "commit", "--quiet", "-m", "squash "+branch)
		mergedInto("main", branch, head, number)
	}
	exists := func(path string) bool {
		_, err := os.Stat(path)
		return !errors.Is(err, os.ErrNotExist)
	}

	done, doneHead := handMade("fix/done")
	squashMerged("fix/done", doneHead, 1)
	held, heldHead := handMade("fix/held")
	squashMerged("fix/held", heldHead, 2)
	folderSession(t, r, "holder", filepath.Join(held, "app"))
	open, _ := handMade("fix/open")
	fake.branches[github.Branch{Repo: acmeApp, Head: "fix/open"}] = []github.PullRequest{{Ref: github.Ref{Repo: acmeApp, Number: 3}, State: "open", HeadRef: "fix/open"}}
	later, laterHead := handMade("fix/later")
	squashMerged("fix/later", laterHead, 4)
	if err := os.WriteFile(filepath.Join(later, "app", "after.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, later, "add", ".")
	gitIn(t, later, "commit", "--quiet", "-m", "after the merge")
	elsewhere, elsewhereHead := handMade("fix/elsewhere")
	mergedInto("release", "fix/elsewhere", elsewhereHead, 5)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		d.runWorkCleans(ctx)
	}()
	testkit.Eventually(t, "the cleanup loop's first pass removes the merged hand-made worktree and its branch", 30*time.Second, func() bool {
		return !exists(done) && gitIn(t, checkout, "branch", "--list", "fix/done") == ""
	})
	cancel()
	<-stopped
	d.sweepHandMadeWorktrees(t.Context())
	for path, why := range map[string]string{held: "an idle session that is not archived works there", open: "its pull request is open", later: "it has a commit after the merged head", elsewhere: "its pull request merged into another branch"} {
		if !exists(path) {
			t.Fatalf("%s was removed though %s", path, why)
		}
	}

	if err := r.ArchiveSession("holder", time.Now()); err != nil {
		t.Fatal(err)
	}
	d.sweepHandMadeWorktrees(t.Context())
	if exists(held) {
		t.Fatal("the worktree was kept after the only session in it was archived")
	}
	if !exists(open) || !exists(later) || !exists(elsewhere) {
		t.Fatal("a later sweep removed a worktree it must keep")
	}
}

func TestTheSweepSearchesAWorktreeProjectAndNeverRemovesAnAddedProjectsFolder(t *testing.T) {
	checkout := pushedCheckout(t)
	d, _, _, _, _ := daemonFixture(t)
	addFolderProject(t, d, checkout)
	gitIn(t, checkout, "remote", "add", "github", "https://github.com/acme/app.git")
	fake := &fakeGitHub{branches: map[github.Branch][]github.PullRequest{}}
	d.github = fake
	merged := func(holder, branch string, number int) string {
		t.Helper()
		path := filepath.Join(holder, worktree.Dir, worktree.Slug(branch))
		gitIn(t, checkout, "worktree", "add", "--quiet", "-b", branch, path)
		gitIn(t, checkout, "config", "branch."+branch+".remote", "github")
		if err := os.WriteFile(filepath.Join(path, "app", worktree.Slug(branch)+".go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, path, "add", ".")
		gitIn(t, path, "commit", "--quiet", "-m", branch)
		head := gitIn(t, path, "rev-parse", "HEAD")
		gitIn(t, checkout, "merge", "--quiet", "--squash", branch)
		gitIn(t, checkout, "commit", "--quiet", "-m", "squash "+branch)
		fake.branches[github.Branch{Repo: acmeApp, Head: branch}] = []github.PullRequest{{Ref: github.Ref{Repo: acmeApp, Number: number}, URL: github.Ref{Repo: acmeApp, Number: number}.URL(), State: "merged", HeadSHA: head, HeadRef: branch, BaseRef: "main"}}
		return path
	}
	project := merged(checkout, "v2/cockpit", 1)
	addFolderProject(t, d, project)
	d.sweepHandMadeWorktrees(t.Context())
	if _, err := os.Stat(project); err != nil {
		t.Fatalf("the sweep removed a worktree added as a project: %v", err)
	}
	inside := merged(project, "v2/inside", 2)
	d.sweepHandMadeWorktrees(t.Context())
	if _, err := os.Stat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the sweep left the merged worktree inside the worktree project: %v", err)
	}
}
