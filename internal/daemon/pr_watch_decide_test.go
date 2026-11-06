package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestCommentsWakeUntilTenInARowThenTheWatchStops(t *testing.T) {
	ref := github.Ref{Repo: acmeApp, Number: 42}
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	row := registry.SessionPR{SessionID: "to", Repo: acmeApp.String(), Number: 42, Watch: true}
	status := openStatus(ref, "aaaaaaa1111")
	status.Remarks = []github.Remark{{ID: "old", Kind: github.RemarkComment, Author: "pat", At: start}}

	verdict := decidePR(row, status, "agent")
	if len(verdict.reasons) != 0 {
		t.Fatalf("the first read woke for old comments: %q", verdict.reasons)
	}
	row = verdict.row
	row.LastReadAt = start

	for i := 1; i <= prCommentWakeLimit; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		status.Remarks = append(status.Remarks,
			github.Remark{ID: fmt.Sprint("mine", i), Kind: github.RemarkComment, Author: "agent", At: at},
			github.Remark{ID: fmt.Sprint("c", i), Kind: github.RemarkComment, Author: "pat", At: at})
		verdict = decidePR(row, status, "agent")
		if len(verdict.reasons) == 0 || !strings.Contains(verdict.reasons[0], "pat commented") || strings.Contains(verdict.reasons[0], ",") {
			t.Fatalf("comment %d reasons = %q", i, verdict.reasons)
		}
		row = verdict.row
		if i < prCommentWakeLimit && !row.Watch {
			t.Fatalf("the watch stopped after %d comment wakes", i)
		}
	}
	if row.Watch || !strings.Contains(verdict.reasons[len(verdict.reasons)-1], "watch stopped") {
		t.Fatalf("after %d comment wakes: watch=%t reasons=%q", prCommentWakeLimit, row.Watch, verdict.reasons)
	}

	row.Watch, row.CommentWakes = true, prCommentWakeLimit-1
	status.Checks = []github.Check{{Name: "test", State: github.CheckFailed}}
	status.Remarks = append(status.Remarks, github.Remark{ID: "late", Kind: github.RemarkComment, Author: "pat", At: start.Add(time.Hour)})
	if verdict = decidePR(row, status, "agent"); !verdict.row.Watch || verdict.row.CommentWakes != 0 {
		t.Fatalf("a check failure with a comment counted as comment-only: %+v", verdict.row)
	}
}

func TestAClosedPullRequestWakesOnceAndStopsItsWatch(t *testing.T) {
	ref := github.Ref{Repo: acmeApp, Number: 42}
	row := registry.SessionPR{SessionID: "to", Repo: acmeApp.String(), Number: 42, Watch: true, State: "open"}
	status := openStatus(ref, "aaaaaaa1111")
	status.State = "closed"
	verdict := decidePR(row, status, "agent")
	if verdict.merged || verdict.row.Watch || len(verdict.reasons) != 1 || !strings.Contains(verdict.reasons[0], "closed without merging") {
		t.Fatalf("closed verdict = %+v", verdict)
	}
	again := verdict.row
	again.Watch = true
	if verdict := decidePR(again, status, "agent"); len(verdict.reasons) != 0 {
		t.Fatalf("a pull request already seen closed woke again: %q", verdict.reasons)
	}
}
