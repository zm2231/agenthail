package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubGH(t *testing.T, script string) GH {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return GH{Path: path}
}

func TestGHSaysWhyItCannotReadPullRequests(t *testing.T) {
	ref := Ref{Repo: Repo{Host: "github.com", Owner: "acme", Name: "app"}, Number: 42}
	cases := []struct {
		name, script, want string
	}{
		{"signed out", `echo "To get started with GitHub CLI, please run:  gh auth login" >&2; exit 4`, "not signed in to github.com"},
		{"bad token", `echo '{"message":"Bad credentials"}'; echo "gh: Bad credentials (HTTP 401)" >&2; exit 1`, "refused gh's sign-in"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := stubGH(t, c.script).PullRequest(t.Context(), ref)
			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) || !strings.Contains(unavailable.Reason, c.want) {
				t.Fatalf("err = %v, want an unavailable reason containing %q", err, c.want)
			}
		})
	}
	if _, err := (GH{Path: filepath.Join(t.TempDir(), "missing")}).PullRequest(t.Context(), ref); err == nil {
		t.Fatal("a missing gh read a pull request")
	}
	notFound := stubGH(t, `echo '{"data":{"r":null},"errors":[{"type":"NOT_FOUND","message":"Could not resolve"}]}'; exit 1`)
	if _, err := notFound.PullRequest(t.Context(), ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGHReadsAPullRequestAndBranchPullRequests(t *testing.T) {
	pr := `{"number":42,"url":"https://github.com/acme/app/pull/42","title":"Fix","state":"OPEN","headRefOid":"abc","headRefName":"ah/x","createdAt":"2026-11-06T10:00:00Z","isCrossRepository":false}`
	fork := `{"number":9,"url":"https://github.com/acme/app/pull/9","title":"Fork","state":"OPEN","headRefOid":"def","headRefName":"ah/x","createdAt":"2026-11-06T10:00:00Z","isCrossRepository":true}`
	gh := stubGH(t, `case "$*" in
*pullRequests*) echo '{"data":{"b0":{"pullRequests":{"nodes":[`+pr+`,`+fork+`]}},"b1":null}}' ;;
*) echo '{"data":{"r":{"pullRequest":`+pr+`}}}' ;;
esac`)
	repo := Repo{Host: "github.com", Owner: "acme", Name: "app"}
	got, err := gh.PullRequest(t.Context(), Ref{Repo: repo, Number: 42})
	if err != nil || got.Title != "Fix" || got.State != "open" || got.HeadSHA != "abc" {
		t.Fatalf("PullRequest = %+v, %v", got, err)
	}
	mine, missing := Branch{Repo: repo, Head: "ah/x"}, Branch{Repo: Repo{Host: "github.com", Owner: "acme", Name: "gone"}, Head: "ah/x"}
	found, err := gh.BranchPullRequests(t.Context(), []Branch{mine, missing})
	if err != nil || len(found[mine]) != 1 || found[mine][0].Number != 42 || len(found[missing]) != 0 {
		t.Fatalf("BranchPullRequests = %+v, %v; want only the same-repository pull request", found, err)
	}
}
