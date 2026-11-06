package daemon

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	prCommentWakeLimit = 10
	prWakeNameLimit    = 5
	prWakeTitleLimit   = 80
	prWakeLimit        = 600
)

type prVerdict struct {
	row     registry.SessionPR
	reasons []string
	merged  bool
}

func decidePR(row registry.SessionPR, status github.Status, viewer string) prVerdict {
	next := row
	next.Title, next.State, next.Mergeable, next.Review, next.Checks = status.Title, status.State, status.Mergeable, status.Review, checkSummary(status.Checks)
	if status.HeadSHA != row.HeadSHA {
		next.FailedChecks = nil
	}
	next.HeadSHA = status.HeadSHA
	next.RemarksThrough, next.RemarkIDs = remarksThrough(row, status.Remarks)
	switch status.State {
	case "merged":
		next.Watch = false
		return prVerdict{row: next, merged: true}
	case "closed":
		next.Watch = false
		if row.State == "closed" {
			return prVerdict{row: next}
		}
		next.Wakes++
		return prVerdict{row: next, reasons: []string{"it was closed without merging, so the watch stopped"}}
	}
	head := shortSHA(status.HeadSHA)
	var reasons []string
	var failed []string
	for _, check := range status.Checks {
		if check.State == github.CheckFailed && !slices.Contains(next.FailedChecks, check.Name) {
			failed = append(failed, check.Name)
		}
	}
	if len(failed) > 0 {
		next.FailedChecks = append(next.FailedChecks, failed...)
		reasons = append(reasons, fmt.Sprintf("checks failed on %s: %s", head, nameList(failed)))
	}
	if checksPassed(status.Checks) && row.PassedHead != status.HeadSHA {
		next.PassedHead = status.HeadSHA
		reasons = append(reasons, fmt.Sprintf("all required checks passed on %s", head))
	}
	if status.Mergeable == "conflicting" && row.ConflictHead != status.HeadSHA {
		next.ConflictHead = status.HeadSHA
		reasons = append(reasons, fmt.Sprintf("%s conflicts with its base branch", head))
	}
	commentOnly := len(reasons) == 0
	if remarks := freshRemarks(row, status.Remarks, viewer); len(remarks) > 0 {
		reasons = append(reasons, remarkLine(remarks))
	}
	if len(reasons) == 0 {
		return prVerdict{row: next}
	}
	next.Wakes++
	if !commentOnly {
		next.CommentWakes = 0
		return prVerdict{row: next, reasons: reasons}
	}
	next.CommentWakes++
	if next.CommentWakes >= prCommentWakeLimit {
		next.Watch = false
		reasons = append(reasons, fmt.Sprintf("that is %d comment-only wakes in a row, so the watch stopped; agenthail_pr watch turns it back on", prCommentWakeLimit))
	}
	return prVerdict{row: next, reasons: reasons}
}

func checksPassed(checks []github.Check) bool {
	considered := checks
	if required := slices.DeleteFunc(slices.Clone(checks), func(c github.Check) bool { return !c.Required }); len(required) > 0 {
		considered = required
	}
	return len(considered) > 0 && !slices.ContainsFunc(considered, func(c github.Check) bool { return c.State != github.CheckPassed })
}

func checkSummary(checks []github.Check) string {
	switch {
	case len(checks) == 0:
		return ""
	case slices.ContainsFunc(checks, func(c github.Check) bool { return c.State == github.CheckFailed }):
		return "failing"
	case slices.ContainsFunc(checks, func(c github.Check) bool { return c.State == github.CheckPending }):
		return "pending"
	}
	return "passing"
}

func freshRemarks(row registry.SessionPR, remarks []github.Remark, viewer string) []github.Remark {
	if row.LastReadAt.IsZero() {
		return nil
	}
	var fresh []github.Remark
	for _, remark := range remarks {
		if remark.Author == viewer {
			continue
		}
		if remark.At.After(row.RemarksThrough) || remark.At.Equal(row.RemarksThrough) && !slices.Contains(row.RemarkIDs, remark.ID) {
			fresh = append(fresh, remark)
		}
	}
	return fresh
}

func remarksThrough(row registry.SessionPR, remarks []github.Remark) (through time.Time, ids []string) {
	through, ids = row.RemarksThrough, row.RemarkIDs
	for _, remark := range remarks {
		switch {
		case remark.At.After(through):
			through, ids = remark.At, []string{remark.ID}
		case remark.At.Equal(through) && !slices.Contains(ids, remark.ID):
			ids = append(slices.Clone(ids), remark.ID)
		}
	}
	return through, ids
}

func remarkLine(remarks []github.Remark) string {
	var parts []string
	for _, remark := range remarks {
		author := remark.Author
		if author == "" {
			author = "someone"
		}
		switch {
		case remark.Kind == github.RemarkReview && remark.State == "approved":
			parts = append(parts, author+" approved")
		case remark.Kind == github.RemarkReview && remark.State == "changes_requested":
			parts = append(parts, author+" requested changes")
		case remark.Kind == github.RemarkReview:
			parts = append(parts, author+" reviewed")
		default:
			parts = append(parts, author+" commented")
		}
	}
	return nameList(parts)
}

func nameList(names []string) string {
	if len(names) <= prWakeNameLimit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:prWakeNameLimit], ", "), len(names)-prWakeNameLimit)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func prWakeMessage(lead string, pr registry.SessionPR, reasons []string) string {
	title := pr.Title
	if len(title) > prWakeTitleLimit {
		title = strings.ToValidUTF8(title[:prWakeTitleLimit], "") + "..."
	}
	message := fmt.Sprintf("[agenthail] %sPull request %s (%s): %s.", lead, pr.URL, title, strings.Join(reasons, "; "))
	if len(message) > prWakeLimit {
		message = strings.ToValidUTF8(message[:prWakeLimit-3], "") + "..."
	}
	return message
}
