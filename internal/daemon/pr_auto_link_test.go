package daemon

import (
	"slices"
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

	if err := d.linkBranchPRs(t.Context()); err != nil {
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
	if err := d.linkBranchPRs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if prs, _ := r.PRsForSession("to"); len(prs) != 0 {
		t.Fatalf("an unlinked pull request came back: %+v", prs)
	}
	if last := fake.branchReads[len(fake.branchReads)-1]; len(last) != 1 || last[0].Head != "ah/y" {
		t.Fatalf("second sweep read %v, want only the branch with no pull request", last)
	}

	fake.err = &github.UnavailableError{Reason: "gh is not signed in to github.com; run gh auth login"}
	if err := d.linkBranchPRs(t.Context()); err == nil {
		t.Fatal("a sweep without a gh sign-in reported success")
	}
}

func TestCreatedPullRequestsComeFromShellCallsWhoseOutputPrintsThem(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	url := "https://github.com/acme/app/pull/42"
	items := []surface.TimelineItem{
		{Kind: "toolCall", Title: "Bash", CallID: "c1", Text: "{\n  \"command\": \"git push -u origin HEAD && gh pr create --fill\",\n  \"description\": \"Open the pull request\"\n}"},
		{Kind: "toolResult", CallID: "c1", Text: url + "\n"},
		{Kind: "toolCall", Title: "exec_command", CallID: "c2", Text: `{"cmd":"gh pr create --fill","workdir":"/w"}`},
		{Kind: "toolResult", CallID: "c2", Status: "error", Text: "https://github.com/acme/app/pull/43\n"},
		{Kind: "toolCall", Title: "exec_command", CallID: "c3", Text: `{"cmd":"gh pr create -B next --fill","workdir":"/w"}`},
		{Kind: "toolResult", CallID: "c3", Text: "https://github.com/acme/app/pull/44\n"},
		{Kind: "toolCall", Title: "Write", CallID: "c4", Text: `{"file_path":"notes.md","content":"gh pr create"}`},
		{Kind: "toolResult", CallID: "c4", Text: "https://github.com/acme/app/pull/45\n"},
	}
	var numbers []int
	for _, ref := range d.createdPRs("to", items) {
		numbers = append(numbers, ref.Number)
	}
	if !slices.Equal(numbers, []int{42, 44}) {
		t.Fatalf("created = %v, want the Claude and Codex shell calls that succeeded", numbers)
	}
}

func TestACreatedPullRequestIsFoundPastAClippedCommandAndOutput(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	body := func(ref, text string) {
		t.Helper()
		entry := registry.SessionJournalEntry{SessionID: "to", Kind: "toolResult", ProviderKey: ref, Payload: []byte(`{}`), BodyRef: ref, FullBody: []byte(text), ObservedAt: time.Now()}
		if _, _, err := r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: 100, Bytes: 8 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	notes := strings.Repeat("a long release note line\n", 1200)
	command := `{"command":"cat > body.md <<'EOF'\n` + strings.ReplaceAll(notes, "\n", `\n`) + `EOF\ngh pr create -F body.md --fill"}`
	output := strings.Repeat("remote: building\n", 20000) + "https://github.com/acme/app/pull/42\n"
	body("call-body", command)
	body("result-body", output)
	items := []surface.TimelineItem{
		{Kind: "toolCall", Title: "Bash", CallID: "c1", Text: command[:16<<10], Truncated: true, BodyRef: "call-body"},
		{Kind: "toolResult", CallID: "c1", Text: output[:16<<10], Truncated: true, BodyRef: "result-body"},
	}
	refs := d.createdPRs("to", items)
	if len(refs) != 1 || refs[0].Number != 42 {
		t.Fatalf("created = %+v, want the pull request printed after %d bytes", refs, len(output))
	}
}
