package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func prSource(p principal) string {
	if p.Kind == principalSession || p.Claimed == daemonapi.CLIActor && p.DashboardBearer {
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
	if request.Item != "" && request.Op != "link" {
		return actionResult{}, invalidRequest("item names the work a pull request is for; only link takes it")
	}
	if request.Item != "" && !d.sessionHoldsItem(*session, request.Item) {
		return actionResult{}, invalidRequest(fmt.Sprintf("%s does not hold work item %s", d.resolveDisplay(session.ID), request.Item))
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
		if err := d.prs.Link(registry.SessionPR{SessionID: session.ID, Repo: repo, Number: ref.Number, URL: pr.URL, Source: prSource(p), Title: pr.Title, State: pr.State, HeadSHA: pr.HeadSHA, ItemID: request.Item}, true); err != nil {
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
		watch, found := request.Op == "watch", false
		_, err := d.changePRs(func(r *registry.Registry) (bool, error) {
			before, linked, err := r.SessionPR(session.ID, repo, ref.Number)
			if err != nil || !linked {
				return false, err
			}
			found, err = r.SetPRWatch(session.ID, repo, ref.Number, watch)
			return found && before.Watch != watch, err
		})
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
		}
		if !found {
			return actionResult{}, actionFailed(http.StatusNotFound, "not_found", fmt.Sprintf("%s is not linked to this session; link it first", ref.URL()))
		}
	}
	prs, err := d.Registry.PRsForSession(session.ID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	return actionOK(map[string]any{"ok": true, "sessionId": session.ID, "prs": d.prs.Views(prs)}), nil
}

func (d *Daemon) sessionHoldsItem(session surface.Session, itemID string) bool {
	held, err := d.heldWork(session)
	return err == nil && slices.ContainsFunc(held, func(h heldWork) bool { return h.item.ID == itemID })
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
