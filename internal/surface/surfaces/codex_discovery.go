package surfaces

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) List(ctx context.Context) ([]surface.Session, error) {
	sessions, _, err := c.list(ctx, false)
	return sessions, err
}

// list reports whether every runtime returned every thread it holds, which
// only a sweep can.
func (c *Codex) list(ctx context.Context, sweep bool) ([]surface.Session, bool, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, false, runtimes.unavailable()
	}
	byID := map[string]surface.Session{}
	var failures []string
	succeeded := false
	complete := sweep
	for _, client := range clients {
		var sessions []surface.Session
		var err error
		if sweep {
			sessions, err = c.listAll(ctx, client, runtimes)
		} else {
			sessions, err = c.listCurrent(ctx, client, runtimes)
		}
		if err != nil {
			failures = append(failures, err.Error())
			complete = false
			continue
		}
		succeeded = true
		for _, session := range sessions {
			if previous, exists := byID[session.ID]; !exists || session.LastActive.After(previous.LastActive) {
				byID[session.ID] = session
			}
		}
	}
	if !succeeded {
		return nil, false, fmt.Errorf("thread/list: %s", strings.Join(failures, "; "))
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		c.reconcileLocalStatus(ctx, &session)
		applyCodexPendingModel(&session)
		out = append(out, session)
	}
	if complete {
		c.forgetUnreadPlans()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out, complete, nil
}

func (c *Codex) Ready(ctx context.Context) error {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return runtimes.unavailable()
	}
	_, err := c.loadedThreadIDs(ctx, clients[0], 1)
	return err
}

func (c *Codex) DesktopReady(ctx context.Context) error {
	client, err := c.openDesktop(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = c.loadedThreadIDs(ctx, client, 1)
	return err
}

func (c *Codex) listCurrent(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	loaded, err := c.listLoaded(ctx, conn, codexRecentListLimit, runtimes)
	if err != nil {
		return nil, err
	}
	recent, err := c.listRecent(ctx, conn, runtimes)
	if err != nil {
		return loaded, nil
	}
	return mergeCodexSessions(loaded, recent), nil
}

// listAll returns every loaded thread and every page of the thread list. A
// failed read or page fails the sweep, since a partial sweep cannot retire
// threads.
func (c *Codex) listAll(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	loaded, readErrors, err := c.readLoaded(ctx, conn, 0, runtimes)
	if err != nil {
		return nil, err
	}
	if len(readErrors) > 0 {
		return nil, fmt.Errorf("read loaded Codex threads: %s", strings.Join(readErrors, "; "))
	}
	var all []surface.Session
	params := codexThreadListParams(codexSweepPageLimit)
	for {
		page, next, err := c.listPageCursor(ctx, conn, params, runtimes)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		params["cursor"] = next
	}
	return mergeCodexSessions(loaded, all), nil
}

func (c *Codex) listLoaded(ctx context.Context, conn codexClient, limit int, runtimes *codexRuntimes) ([]surface.Session, error) {
	sessions, readErrors, err := c.readLoaded(ctx, conn, limit, runtimes)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 && len(readErrors) > 0 {
		return nil, fmt.Errorf("read loaded Codex threads: %s", strings.Join(readErrors, "; "))
	}
	return sessions, nil
}

func (c *Codex) readLoaded(ctx context.Context, conn codexClient, limit int, runtimes *codexRuntimes) ([]surface.Session, []string, error) {
	ids, err := c.loadedThreadIDs(ctx, conn, limit)
	if err != nil {
		return nil, nil, err
	}
	sessions := make([]surface.Session, 0, len(ids))
	var readErrors []string
	for _, id := range ids {
		thread, err := c.readThreadObject(ctx, conn, id)
		if err != nil {
			readErrors = append(readErrors, err.Error())
			continue
		}
		if codexListsThread(thread) {
			sessions = append(sessions, runtimes.session(thread))
		}
	}
	return sessions, readErrors, nil
}

func (c *Codex) loadedThreadIDs(ctx context.Context, conn codexClient, limit int) ([]string, error) {
	params := map[string]any{}
	if limit > 0 {
		params["limit"] = limit
	}
	resp, err := conn.Request(ctx, "thread/loaded/list", params, 5*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	values, _ := result["data"].([]any)
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id := loadedThreadID(value)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func loadedThreadID(value any) string {
	if id, ok := value.(string); ok {
		return id
	}
	entry, _ := value.(map[string]any)
	if id := str(entry, "id"); id != "" {
		return id
	}
	thread, _ := entry["thread"].(map[string]any)
	return str(thread, "id")
}

func mergeCodexSessions(primary, secondary []surface.Session) []surface.Session {
	byID := make(map[string]surface.Session, len(primary)+len(secondary))
	for _, session := range secondary {
		byID[session.ID] = session
	}
	for _, session := range primary {
		byID[session.ID] = session
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

func (c *Codex) listRecent(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	return c.listPage(ctx, conn, codexThreadListParams(codexRecentListLimit), runtimes)
}

// codexThreadListParams asks for every conversation a person or Agenthail can
// address. thread/list defaults to interactive sources and filters by model
// provider unless told otherwise, which would hide spawned subagents,
// app-server threads and threads under other providers. Exec runs and Codex's
// own review, compaction and memory subagents stay out.
func codexThreadListParams(limit int) map[string]any {
	return map[string]any{
		"limit":          limit,
		"sortKey":        "recency_at",
		"sortDirection":  "desc",
		"useStateDbOnly": true,
		"sourceKinds":    []string{"cli", "vscode", "appServer", "subAgentThreadSpawn"},
		"modelProviders": []string{},
	}
}

func (c *Codex) listPage(ctx context.Context, conn codexClient, params map[string]any, runtimes *codexRuntimes) ([]surface.Session, error) {
	sessions, _, err := c.listPageCursor(ctx, conn, params, runtimes)
	return sessions, err
}

func (c *Codex) listPageCursor(ctx context.Context, conn codexClient, params map[string]any, runtimes *codexRuntimes) ([]surface.Session, string, error) {
	resp, err := conn.Request(ctx, "thread/list", params, 10*time.Second)
	if err != nil {
		return nil, "", err
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	sessions := make([]surface.Session, 0, len(threads))
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		if codexListsThread(thread) {
			sessions = append(sessions, runtimes.session(thread))
		}
	}
	return sessions, str(result, "nextCursor"), nil
}
