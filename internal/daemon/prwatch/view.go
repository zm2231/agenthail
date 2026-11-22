package prwatch

import (
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

// View is a linked pull request as a session row shows it.
type View struct {
	Repo      string `json:"repo"`
	Number    int    `json:"number"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Watch     bool   `json:"watch"`
	Title     string `json:"title,omitempty"`
	State     string `json:"state,omitempty"`
	HeadSHA   string `json:"headSha,omitempty"`
	Mergeable string `json:"mergeable,omitempty"`
	Review    string `json:"review,omitempty"`
	Checks    string `json:"checks,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

func (w *Watcher) Views(prs []registry.SessionPR) []View {
	views := make([]View, 0, len(prs))
	for _, pr := range prs {
		views = append(views, View{Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Source: pr.Source, Watch: pr.Watch, Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA,
			Mergeable: pr.Mergeable, Review: pr.Review, Checks: pr.Checks, Problem: w.Problem(pr)})
	}
	return views
}

// Problem is why a watched pull request is not being read: its GitHub host
// refuses or rate limits reads, or GitHub stopped returning it.
func (w *Watcher) Problem(pr registry.SessionPR) string {
	if !pr.Watch {
		return ""
	}
	if repo, ok := github.ParseRepo(pr.Repo); ok {
		if problem := w.state.problem(repo.Host); problem != "" {
			return problem
		}
	}
	if pr.ReadFailures >= registry.PRReadFailureLimit {
		return "GitHub has not returned this pull request on the last few reads"
	}
	return ""
}
