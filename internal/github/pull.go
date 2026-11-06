package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type PullRequest struct {
	Ref
	URL       string
	Title     string
	State     string
	HeadSHA   string
	HeadRef   string
	CreatedAt time.Time
}

type Branch struct {
	Repo
	Head string
}

type Client interface {
	PullRequest(ctx context.Context, ref Ref) (PullRequest, error)
	// BranchPullRequests reads, in one request per GitHub host, the pull
	// requests opened from each branch within its own repository.
	BranchPullRequests(ctx context.Context, branches []Branch) (map[Branch][]PullRequest, error)
}

const pullFields = `number url title state headRefOid headRefName createdAt isCrossRepository`

type pullNode struct {
	Number            int       `json:"number"`
	URL               string    `json:"url"`
	Title             string    `json:"title"`
	State             string    `json:"state"`
	HeadRefOid        string    `json:"headRefOid"`
	HeadRefName       string    `json:"headRefName"`
	CreatedAt         time.Time `json:"createdAt"`
	IsCrossRepository bool      `json:"isCrossRepository"`
}

func (n pullNode) pullRequest(repo Repo) PullRequest {
	return PullRequest{Ref: Ref{Repo: repo, Number: n.Number}, URL: n.URL, Title: n.Title, State: strings.ToLower(n.State), HeadSHA: n.HeadRefOid, HeadRef: n.HeadRefName, CreatedAt: n.CreatedAt}
}

func (g GH) PullRequest(ctx context.Context, ref Ref) (PullRequest, error) {
	query := fmt.Sprintf(`query{r:repository(owner:%s,name:%s){pullRequest(number:%d){%s}}}`, quote(ref.Owner), quote(ref.Name), ref.Number, pullFields)
	reply, err := g.graphql(ctx, ref.Host, query)
	if err != nil {
		return PullRequest{}, err
	}
	var repo struct {
		PullRequest *pullNode `json:"pullRequest"`
	}
	if raw := reply.Data["r"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &repo); err != nil {
			return PullRequest{}, err
		}
	}
	if repo.PullRequest == nil {
		for _, failure := range reply.Errors {
			if failure.Type != "NOT_FOUND" {
				return PullRequest{}, fmt.Errorf("gh api graphql: %s", failure.Message)
			}
		}
		return PullRequest{}, ErrNotFound
	}
	return repo.PullRequest.pullRequest(ref.Repo), nil
}

func (g GH) BranchPullRequests(ctx context.Context, branches []Branch) (map[Branch][]PullRequest, error) {
	byHost := map[string][]Branch{}
	for _, branch := range branches {
		byHost[branch.Host] = append(byHost[branch.Host], branch)
	}
	found := map[Branch][]PullRequest{}
	for host, group := range byHost {
		var query strings.Builder
		query.WriteString("query{")
		for i, branch := range group {
			fmt.Fprintf(&query, `b%d:repository(owner:%s,name:%s){pullRequests(headRefName:%s,first:5,orderBy:{field:CREATED_AT,direction:DESC}){nodes{%s}}}`,
				i, quote(branch.Owner), quote(branch.Repo.Name), quote(branch.Head), pullFields)
		}
		query.WriteString("}")
		reply, err := g.graphql(ctx, host, query.String())
		if err != nil {
			return nil, err
		}
		for i, branch := range group {
			raw := reply.Data[fmt.Sprintf("b%d", i)]
			if len(raw) == 0 || string(raw) == "null" {
				continue
			}
			var repo struct {
				PullRequests struct {
					Nodes []pullNode `json:"nodes"`
				} `json:"pullRequests"`
			}
			if err := json.Unmarshal(raw, &repo); err != nil {
				return nil, err
			}
			for _, node := range repo.PullRequests.Nodes {
				if !node.IsCrossRepository && node.HeadRefName == branch.Head {
					found[branch] = append(found[branch], node.pullRequest(branch.Repo))
				}
			}
		}
	}
	return found, nil
}
