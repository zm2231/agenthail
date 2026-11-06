package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGHWatchesEveryPullRequestOnAHostInOneQuery(t *testing.T) {
	queries := filepath.Join(t.TempDir(), "queries")
	open := `{"number":42,"title":"Fix","state":"OPEN","merged":false,"mergeable":"CONFLICTING","headRefOid":"abc","headRefName":"ah/x","reviewDecision":"CHANGES_REQUESTED",
"commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[
{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","isRequired":true},
{"__typename":"CheckRun","name":"lint","status":"IN_PROGRESS","conclusion":"","isRequired":false},
{"__typename":"StatusContext","context":"ci/legacy","state":"SUCCESS","isRequired":false}]}}}}]},
"reviews":{"nodes":[{"id":"R1","state":"CHANGES_REQUESTED","submittedAt":"2026-11-06T10:00:00Z","author":{"login":"pat"}},{"id":"R2","state":"PENDING","submittedAt":"2026-11-06T10:01:00Z","author":{"login":"pat"}}]},
"comments":{"nodes":[{"id":"C1","createdAt":"2026-11-06T10:02:00Z","author":null}]}}`
	merged := `{"number":7,"title":"Done","state":"CLOSED","merged":true,"mergeable":"UNKNOWN","headRefOid":"def","headRefName":"ah/y","reviewDecision":"","commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]},"reviews":{"nodes":[]},"comments":{"nodes":[]}}`
	gh := stubGH(t, `printf '%s\n' "$*" >> `+queries+`
echo '{"data":{"viewer":{"login":"agent"},"rateLimit":{"remaining":4321,"resetAt":"2026-11-06T11:00:00Z"},"r0":{"p42":`+strings.ReplaceAll(open, "\n", "")+`,"p7":`+merged+`,"p9":null}}}'`)
	repo := Repo{Host: "github.com", Owner: "acme", Name: "app"}
	fix, done, gone := Ref{Repo: repo, Number: 42}, Ref{Repo: repo, Number: 7}, Ref{Repo: repo, Number: 9}

	watch, err := gh.Watch(t.Context(), "github.com", []Ref{fix, done, gone, fix})
	if err != nil {
		t.Fatal(err)
	}
	ran, _ := os.ReadFile(queries)
	if lines := strings.Count(string(ran), "\n"); lines != 1 || strings.Count(string(ran), "p42:") != 1 {
		t.Fatalf("gh ran %d times: %s", lines, ran)
	}
	if watch.Viewer != "agent" || watch.RateRemaining != 4321 || watch.RateResetAt.IsZero() {
		t.Fatalf("watch = %+v", watch)
	}
	got := watch.Statuses[fix]
	if got.State != "open" || got.Mergeable != "conflicting" || got.Review != "changes_requested" || got.HeadRef != "ah/x" {
		t.Fatalf("open status = %+v", got)
	}
	wantChecks := []Check{{"test", true, CheckFailed}, {"lint", false, CheckPending}, {"ci/legacy", false, CheckPassed}}
	if len(got.Checks) != len(wantChecks) {
		t.Fatalf("checks = %+v", got.Checks)
	}
	for i, check := range wantChecks {
		if got.Checks[i] != check {
			t.Fatalf("check %d = %+v, want %+v", i, got.Checks[i], check)
		}
	}
	if len(got.Remarks) != 2 || got.Remarks[0].Author != "pat" || got.Remarks[0].State != "changes_requested" || got.Remarks[1].Kind != RemarkComment {
		t.Fatalf("remarks = %+v, want the submitted review and the comment", got.Remarks)
	}
	if watch.Statuses[done].State != "merged" {
		t.Fatalf("merged status = %+v", watch.Statuses[done])
	}
	if _, found := watch.Statuses[gone]; found {
		t.Fatal("a pull request GitHub did not return has a status")
	}

	limited := stubGH(t, `echo "gh: API rate limit exceeded for user" >&2; exit 1`)
	var rate *RateLimitedError
	if _, err := limited.Watch(t.Context(), "github.com", []Ref{fix}); !errors.As(err, &rate) {
		t.Fatalf("err = %v, want a rate limit", err)
	}
}
