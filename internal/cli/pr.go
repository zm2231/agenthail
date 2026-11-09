package cli

import (
	"context"
	"fmt"
)

const prUsage = `usage: agenthail pr <link|unlink|watch|unwatch> <url|number> [--repo <owner/name>] [--session <target>] [--json]`

type prAnswer struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"sessionId"`
	PRs       []struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
		URL    string `json:"url"`
		Source string `json:"source"`
		Watch  bool   `json:"watch"`
		Title  string `json:"title,omitempty"`
		State  string `json:"state,omitempty"`
	} `json:"prs"`
}

func (a *App) cmdPR(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 2 {
		return fmt.Errorf("%s", prUsage)
	}
	ctx, cancel, err := a.deadlineContext(a.DefaultTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	id, err := a.prSession(ctx, flagVal(args, "--session"))
	if err != nil {
		return err
	}
	if err := a.controlScopeOf(ctx, id); err != nil {
		return err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	request := map[string]any{"action": "pr", "sessionId": id, "op": positional[0], "pr": positional[1]}
	if repo := flagVal(args, "--repo"); repo != "" {
		request["repo"] = repo
	}
	var answer prAnswer
	if err := projectAction(ctx, client, request, &answer); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(answer)
	}
	if len(answer.PRs) == 0 {
		fmt.Println("no pull requests linked")
		return nil
	}
	for _, pr := range answer.PRs {
		watch := "not watched"
		if pr.Watch {
			watch = "watched"
		}
		fmt.Printf("%s  %s  %s  %s\n", pr.URL, pr.State, watch, pr.Title)
	}
	return nil
}

func (a *App) prSession(ctx context.Context, target string) (string, error) {
	if target != "" {
		session, _, remote, err := a.resolveAnyTarget(ctx, target)
		if err != nil {
			return "", err
		}
		if remote != nil {
			return remote.ID, nil
		}
		return session.ID, nil
	}
	card, err := a.callerCard()
	if err != nil {
		return "", err
	}
	if !card.Resolved {
		return "", fmt.Errorf("cannot tell which session is calling (%s); pass --session <target>", card.Reason)
	}
	return card.Session, nil
}
