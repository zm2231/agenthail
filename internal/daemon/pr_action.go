package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type prView struct {
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

func (d *Daemon) changePRs(write func(*registry.Registry) (bool, error)) (bool, error) {
	changed, err := write(d.Registry)
	if err == nil && changed {
		d.requestCatalogDiscovery()
	}
	return changed, err
}

func (d *Daemon) prViews(prs []registry.SessionPR) []prView {
	views := make([]prView, 0, len(prs))
	for _, pr := range prs {
		views = append(views, prView{Repo: pr.Repo, Number: pr.Number, URL: pr.URL, Source: pr.Source, Watch: pr.Watch, Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA,
			Mergeable: pr.Mergeable, Review: pr.Review, Checks: pr.Checks, Problem: d.prProblem(pr)})
	}
	return views
}

func (d *Daemon) prProblem(pr registry.SessionPR) string {
	if !pr.Watch {
		return ""
	}
	if repo, ok := github.ParseRepo(pr.Repo); ok {
		if problem := d.prWatch.problem(repo.Host); problem != "" {
			return problem
		}
	}
	if pr.ReadFailures >= prReadFailureLimit {
		return "GitHub has not returned this pull request on the last few reads"
	}
	return ""
}

func prSource(p principal) string {
	if p.Kind == principalSession || p.Claimed == CLIActor && p.DashboardBearer {
		return registry.PRSourceAgent
	}
	return registry.PRSourcePerson
}

func (d *Daemon) doPR(ctx context.Context, p principal, session *surface.Session, request dashboardActionRequest) (actionResult, error) {
	switch request.Op {
	case "link", "unlink", "watch", "unwatch":
	default:
		return actionResult{}, invalidRequest("op must be link, unlink, watch or unwatch")
	}
	ref, err := d.resolvePRRef(ctx, session, request.PR, request.Repo)
	if err != nil {
		return actionResult{}, err
	}
	repo := ref.Repo.String()
	switch request.Op {
	case "link":
		pr, err := d.github.PullRequest(ctx, ref)
		if err != nil {
			return actionResult{}, githubFailure(ref, err)
		}
		if _, err := d.changePRs(func(r *registry.Registry) (bool, error) {
			_, err := r.LinkPR(registry.SessionPR{SessionID: session.ID, Repo: repo, Number: ref.Number, URL: pr.URL, Source: prSource(p), Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA}, true, time.Now())
			return err == nil, err
		}); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
	case "unlink":
		changed, err := d.changePRs(func(r *registry.Registry) (bool, error) { return r.UnlinkPR(session.ID, repo, ref.Number) })
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
		if !changed {
			return actionResult{}, actionFailed(http.StatusNotFound, "not_found", fmt.Sprintf("%s is not linked to this session", ref.URL()))
		}
	default:
		changed, err := d.changePRs(func(r *registry.Registry) (bool, error) {
			return r.SetPRWatch(session.ID, repo, ref.Number, request.Op == "watch")
		})
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
		if !changed {
			return actionResult{}, actionFailed(http.StatusNotFound, "not_found", fmt.Sprintf("%s is not linked to this session; link it first", ref.URL()))
		}
	}
	prs, err := d.Registry.PRsForSession(session.ID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "sessionId": session.ID, "prs": d.prViews(prs)}), nil
}

func githubFailure(ref github.Ref, err error) error {
	var unavailable *github.UnavailableError
	switch {
	case errors.As(err, &unavailable):
		return actionFailed(http.StatusConflict, "github_refused", "cannot read pull requests: "+unavailable.Reason)
	case errors.Is(err, github.ErrNotFound):
		return actionFailed(http.StatusNotFound, "not_found", fmt.Sprintf("%s was not found, or gh's sign-in cannot see it", ref.URL()))
	}
	return actionFailed(http.StatusBadGateway, "github_failed", err.Error())
}

func (d *Daemon) resolvePRRef(ctx context.Context, session *surface.Session, raw, repoArg string) (github.Ref, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if raw == "" {
		return github.Ref{}, invalidRequest("pr is required: a pull request URL, or its number")
	}
	if ref, ok := github.ParseURL(raw); ok {
		return ref, nil
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		return github.Ref{}, invalidRequest("pr must be a pull request URL or a number")
	}
	if repoArg != "" {
		repo, ok := github.ParseRepo(repoArg)
		if !ok {
			return github.Ref{}, invalidRequest("repo must be owner/name or host/owner/name")
		}
		return github.Ref{Repo: repo, Number: number}, nil
	}
	linked, err := d.Registry.PRsForSession(session.ID)
	if err != nil {
		return github.Ref{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	var matches []github.Ref
	for _, pr := range linked {
		if repo, ok := github.ParseRepo(pr.Repo); ok && pr.Number == number {
			matches = append(matches, github.Ref{Repo: repo, Number: number})
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
	default:
		return github.Ref{}, invalidRequest(fmt.Sprintf("this session has pull request %d in more than one repository; pass repo", number))
	}
	repo, err := d.sessionRepo(ctx, session)
	if err != nil {
		return github.Ref{}, err
	}
	return github.Ref{Repo: repo, Number: number}, nil
}

func (d *Daemon) sessionRepo(ctx context.Context, session *surface.Session) (github.Repo, error) {
	dir := session.Cwd
	if record, found, err := d.Registry.WorkForSession(session.ID); err == nil && found {
		dir = record.WorktreePath
	}
	if dir == "" {
		return github.Repo{}, invalidRequest("this session has no folder to find its repository from; pass repo")
	}
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return github.Repo{}, invalidRequest("the session's folder has no origin remote; pass repo")
	}
	repo, ok := github.ParseRepo(string(out))
	if !ok {
		return github.Repo{}, invalidRequest(fmt.Sprintf("origin %s is not a GitHub repository; pass repo", strings.TrimSpace(string(out))))
	}
	return repo, nil
}
