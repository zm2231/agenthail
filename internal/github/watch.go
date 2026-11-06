package github

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	CheckPending = "pending"
	CheckPassed  = "passed"
	CheckFailed  = "failed"

	RemarkReview  = "review"
	RemarkComment = "comment"
)

type Check struct {
	Name     string
	Required bool
	State    string
}

type Remark struct {
	ID     string
	Kind   string
	Author string
	State  string
	At     time.Time
}

type Status struct {
	Ref
	Title     string
	State     string
	HeadSHA   string
	HeadRef   string
	Mergeable string
	Review    string
	Checks    []Check
	Remarks   []Remark
}

type Watch struct {
	Viewer        string
	Statuses      map[Ref]Status
	RateRemaining int
	RateResetAt   time.Time
}

type RateLimitedError struct {
	Message string
}

func (e *RateLimitedError) Error() string { return e.Message }

const watchFields = `number title state merged mergeable headRefOid headRefName reviewDecision ` +
	`commits(last:1){nodes{commit{statusCheckRollup{contexts(first:100){nodes{__typename ` +
	`...on CheckRun{name status conclusion isRequired(pullRequestNumber:%d)} ` +
	`...on StatusContext{context state isRequired(pullRequestNumber:%d)}}}}}}} ` +
	`reviews(last:20){nodes{id state submittedAt author{login}}} ` +
	`comments(last:20){nodes{id createdAt author{login}}}`

type watchNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	Mergeable      string `json:"mergeable"`
	HeadRefOid     string `json:"headRefOid"`
	HeadRefName    string `json:"headRefName"`
	ReviewDecision string `json:"reviewDecision"`
	Commits        struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					Contexts struct {
						Nodes []struct {
							Typename   string `json:"__typename"`
							Name       string `json:"name"`
							Status     string `json:"status"`
							Conclusion string `json:"conclusion"`
							Context    string `json:"context"`
							State      string `json:"state"`
							IsRequired bool   `json:"isRequired"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Reviews struct {
		Nodes []struct {
			ID          string    `json:"id"`
			State       string    `json:"state"`
			SubmittedAt time.Time `json:"submittedAt"`
			Author      *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		Nodes []struct {
			ID        string    `json:"id"`
			CreatedAt time.Time `json:"createdAt"`
			Author    *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
}

func (n watchNode) status(ref Ref) Status {
	status := Status{Ref: ref, Title: n.Title, State: strings.ToLower(n.State), HeadSHA: n.HeadRefOid, HeadRef: n.HeadRefName, Mergeable: strings.ToLower(n.Mergeable), Review: "none"}
	if n.Merged {
		status.State = "merged"
	}
	switch n.ReviewDecision {
	case "APPROVED":
		status.Review = "approved"
	case "CHANGES_REQUESTED":
		status.Review = "changes_requested"
	}
	for _, commit := range n.Commits.Nodes {
		if commit.Commit.StatusCheckRollup == nil {
			continue
		}
		for _, context := range commit.Commit.StatusCheckRollup.Contexts.Nodes {
			check := Check{Required: context.IsRequired}
			if context.Typename == "StatusContext" {
				check.Name, check.State = context.Context, statusContextState(context.State)
			} else {
				check.Name, check.State = context.Name, checkRunState(context.Status, context.Conclusion)
			}
			status.Checks = append(status.Checks, check)
		}
	}
	for _, review := range n.Reviews.Nodes {
		if review.State == "PENDING" {
			continue
		}
		status.Remarks = append(status.Remarks, Remark{ID: review.ID, Kind: RemarkReview, Author: login(review.Author), State: strings.ToLower(review.State), At: review.SubmittedAt})
	}
	for _, comment := range n.Comments.Nodes {
		status.Remarks = append(status.Remarks, Remark{ID: comment.ID, Kind: RemarkComment, Author: login(comment.Author), At: comment.CreatedAt})
	}
	return status
}

func login(author *struct {
	Login string `json:"login"`
}) string {
	if author == nil {
		return ""
	}
	return author.Login
}

func checkRunState(status, conclusion string) string {
	if status != "COMPLETED" {
		return CheckPending
	}
	switch conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return CheckPassed
	}
	return CheckFailed
}

func statusContextState(state string) string {
	switch state {
	case "SUCCESS":
		return CheckPassed
	case "FAILURE", "ERROR":
		return CheckFailed
	}
	return CheckPending
}

func (g GH) Watch(ctx context.Context, host string, refs []Ref) (Watch, error) {
	byRepo := map[Repo][]Ref{}
	var repos []Repo
	for _, ref := range refs {
		if ref.Host != host {
			return Watch{}, fmt.Errorf("%s is not on %s", ref.URL(), host)
		}
		if _, seen := byRepo[ref.Repo]; !seen {
			repos = append(repos, ref.Repo)
		}
		if !slices.Contains(byRepo[ref.Repo], ref) {
			byRepo[ref.Repo] = append(byRepo[ref.Repo], ref)
		}
	}
	var query strings.Builder
	query.WriteString("query{viewer{login} rateLimit{remaining resetAt}")
	for i, repo := range repos {
		fmt.Fprintf(&query, ` r%d:repository(owner:%s,name:%s){`, i, quote(repo.Owner), quote(repo.Name))
		for _, ref := range byRepo[repo] {
			fmt.Fprintf(&query, ` p%d:pullRequest(number:%d){`+watchFields+`}`, ref.Number, ref.Number, ref.Number, ref.Number)
		}
		query.WriteString("}")
	}
	query.WriteString("}")
	reply, err := g.graphql(ctx, host, query.String())
	if err != nil {
		return Watch{}, err
	}
	watch := Watch{Statuses: map[Ref]Status{}}
	var viewer struct {
		Login string `json:"login"`
	}
	_ = json.Unmarshal(reply.Data["viewer"], &viewer)
	watch.Viewer = viewer.Login
	var rate struct {
		Remaining int       `json:"remaining"`
		ResetAt   time.Time `json:"resetAt"`
	}
	if json.Unmarshal(reply.Data["rateLimit"], &rate) == nil {
		watch.RateRemaining, watch.RateResetAt = rate.Remaining, rate.ResetAt
	}
	for i, repo := range repos {
		var pulls map[string]*watchNode
		if raw := reply.Data[fmt.Sprintf("r%d", i)]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &pulls); err != nil {
				return Watch{}, err
			}
		}
		for _, ref := range byRepo[repo] {
			if node := pulls[fmt.Sprintf("p%d", ref.Number)]; node != nil {
				watch.Statuses[ref] = node.status(ref)
			}
		}
	}
	return watch, nil
}
