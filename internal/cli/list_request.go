package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/zm2231/agenthail/internal/daemon"
)

func (a *App) cmdList(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail list [--all|--archived|--recent|--running] [--project <name|id>|--cwd <path>] [--wide] [--json]")
	}
	scope, err := listScopeFromArgs(args)
	if err != nil {
		return err
	}
	project, cwd := flagVal(args, "--project"), flagVal(args, "--cwd")
	if project != "" && cwd != "" {
		return fmt.Errorf("choose either --cwd or --project")
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	timeout := a.DefaultTimeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	query := url.Values{"scope": {scope}, "limit": {"200"}}
	if project != "" {
		id, err := listProjectID(ctx, client, project, scope)
		if err != nil {
			return err
		}
		query.Set("projectId", id)
	} else if cwd != "" {
		id, err := a.selectedProject(ctx, nil, "", cwd)
		if err != nil {
			return err
		}
		query.Set("projectId", id)
	}
	state, err := readListView(ctx, client, query)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emit(state)
	}
	return renderListView(state, hasFlag(args, "--wide"))
}

func readListView(ctx context.Context, client *daemonAPIClient, query url.Values) (daemon.SessionViewSnapshot, error) {
	var result daemon.SessionViewSnapshot
	seen := map[string]bool{}
	for {
		var page daemon.SessionViewSnapshot
		if err := client.getJSON(ctx, "/api/v1/snapshot", query, &page); err != nil {
			return result, err
		}
		if page.View == nil || page.View.Revision == "" {
			return result, fmt.Errorf("daemon did not return a session view; update the daemon")
		}
		if page.View.Scope != query.Get("scope") {
			return result, fmt.Errorf("daemon returned a different session scope")
		}
		if _, ok := page.View.Counts[page.View.Scope]; !ok {
			return result, fmt.Errorf("daemon did not return view counts")
		}
		for _, row := range page.Sessions {
			if row.Display == nil || row.Display.Label == "" {
				return result, fmt.Errorf("daemon did not return session display fields")
			}
		}
		if result.View == nil {
			result = page
		} else {
			if result.View.Revision != page.View.Revision {
				return result, fmt.Errorf("catalog changed while listing; retry the command")
			}
			result.View.Families = append(result.View.Families, page.View.Families...)
			result.Sessions = append(result.Sessions, page.Sessions...)
		}
		if page.NextCursor == "" {
			break
		}
		if seen[page.NextCursor] {
			return result, fmt.Errorf("daemon repeated a catalog cursor")
		}
		seen[page.NextCursor] = true
		query.Set("cursor", page.NextCursor)
	}
	query.Del("cursor")
	result.NextCursor = ""
	loaded := map[string]bool{}
	for _, row := range result.Sessions {
		loaded[row.ID] = true
	}
	missing := []string{}
	for _, family := range result.View.Families {
		for _, member := range family.Members {
			if !loaded[member.ID] {
				missing = append(missing, member.ID)
				loaded[member.ID] = true
			}
		}
	}
	for len(missing) > 0 {
		n := min(len(missing), 200)
		query["sessionId"] = missing[:n]
		query.Set("revision", result.View.Revision)
		var page daemon.SessionViewSnapshot
		if err := client.getJSON(ctx, "/api/v1/snapshot", query, &page); err != nil {
			return result, err
		}
		if page.View == nil || page.View.Revision != result.View.Revision {
			return result, fmt.Errorf("catalog changed while reading members")
		}
		for _, row := range page.Sessions {
			if row.Display == nil || row.Display.Label == "" {
				return result, fmt.Errorf("missing session display for %s", row.ID)
			}
		}
		result.Sessions = append(result.Sessions, page.Sessions...)
		missing = missing[n:]
	}
	index := map[string]int{}
	for i, row := range result.Sessions {
		index[row.ID] = i
	}
	ordered := result.Sessions[:0:0]
	for _, family := range result.View.Families {
		for _, member := range family.Members {
			at, ok := index[member.ID]
			if !ok {
				return result, fmt.Errorf("daemon omitted session %s", member.ID)
			}
			ordered = append(ordered, result.Sessions[at])
		}
	}
	result.Sessions = ordered
	return result, nil
}
