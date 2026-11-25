package codexsurface

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func (c *Codex) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	if surface.LooksLikeUUID(target) {
		return c.resolveID(ctx, target)
	}
	sessions, err := c.resolveCandidates(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	for _, session := range sessions {
		if strings.EqualFold(session.Name, target) {
			exactMatches = append(exactMatches, session)
			continue
		}
		if strings.HasPrefix(session.ID, target) ||
			strings.Contains(strings.ToLower(session.Cwd), lower) ||
			strings.Contains(strings.ToLower(session.Name), lower) {
			matches = append(matches, session)
		}
	}
	if len(exactMatches) > 0 {
		matches = exactMatches
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no codex session matched '%s'", target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s cwd=%s", m.ID, m.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	return &matches[0], nil
}

func (c *Codex) resolveID(ctx context.Context, id string) (*surface.Session, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	reader := runtimes.reader(id)
	if reader == nil {
		return nil, runtimes.unavailable()
	}
	thread, err := c.readThreadObject(ctx, reader, id)
	if err != nil {
		return nil, err
	}
	session := runtimes.session(thread)
	if session.ID == "" {
		session.ID = id
	}
	return &session, nil
}

func (c *Codex) readThreadObject(ctx context.Context, conn codexClient, id string) (map[string]any, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	if thread == nil {
		return nil, fmt.Errorf("thread/read response missing thread")
	}
	if receipt, ok := result[codexReadStateKey]; ok {
		thread[codexReadStateKey] = receipt
	}
	return thread, nil
}

// Codex's title filter is case-sensitive and ignores cwd and ID, so the resolver matches every addressable thread itself.
func (c *Codex) resolveCandidates(ctx context.Context) ([]surface.Session, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, runtimes.unavailable()
	}
	var err error
	for _, client := range clients {
		var sessions []surface.Session
		if sessions, err = c.listAddressable(ctx, client, runtimes); err == nil {
			return sessions, nil
		}
	}
	return nil, err
}

func (c *Codex) listAddressable(ctx context.Context, client codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	params := codexThreadListParams(codexSweepPageLimit)
	var all []surface.Session
	for {
		page, next, err := c.listPageCursor(ctx, client, params, runtimes)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" || next == params["cursor"] {
			return all, nil
		}
		params["cursor"] = next
	}
}

func (c *Codex) SearchSessions(ctx context.Context, query string, limit int, found func(surface.SessionSearchResult)) ([]surface.SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("Codex history search requires a query")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	var clients []codexClient
	for _, client := range []codexClient{runtimes.managed, runtimes.desktop} {
		if client != nil {
			clients = append(clients, client)
		}
	}
	if len(clients) == 0 {
		return nil, runtimes.unavailable()
	}
	params := map[string]any{
		"searchTerm":    query,
		"limit":         limit,
		"sortKey":       "recency_at",
		"sortDirection": "desc",
		"archived":      false,
	}
	seen := map[string]bool{}
	partial := func(entry map[string]any) {
		if result, ok := runtimes.searchResult(entry); ok && found != nil && !seen[result.Session.ID] {
			seen[result.Session.ID] = true
			found(result)
		}
	}
	var response map[string]any
	var err error
	for _, client := range clients {
		if response, err = searchHistory(ctx, client, params, partial); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		if codexMethodMissing(err) {
			return nil, surface.ErrUnsupported
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Codex history search did not finish in time: %w", ctx.Err())
		}
		return nil, fmt.Errorf("Codex history search: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	output := make([]surface.SessionSearchResult, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		if result, ok := runtimes.searchResult(entry); ok {
			output = append(output, result)
		}
	}
	return output, nil
}

// The connection carries only this search, so every partial on it is this search's.
func searchHistory(ctx context.Context, client codexClient, params map[string]any, partial func(map[string]any)) (map[string]any, error) {
	notifier, streams := client.(codexNotifier)
	if !streams {
		return client.Request(ctx, "thread/search", params, surface.SessionSearchBudget)
	}
	type reply struct {
		response map[string]any
		err      error
	}
	done := make(chan reply, 1)
	go func() {
		response, err := client.Request(ctx, "thread/search/stream", params, surface.SessionSearchBudget)
		done <- reply{response, err}
	}()
	drain := func() {
		for _, event := range notifier.DrainNotifications() {
			if event.Method != "thread/search/partial" {
				continue
			}
			entries, _ := event.Params["data"].([]any)
			for _, entry := range entries {
				if value, ok := entry.(map[string]any); ok {
					partial(value)
				}
			}
		}
	}
	for {
		select {
		case <-notifier.Notified():
			drain()
		case result := <-done:
			drain()
			if result.err != nil && codexMethodMissing(result.err) && ctx.Err() == nil {
				return client.Request(ctx, "thread/search", params, surface.SessionSearchBudget)
			}
			return result.response, result.err
		}
	}
}

func codexMethodMissing(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "method not found") || strings.Contains(err.Error(), "-32601")
}

func (r *codexRuntimes) searchResult(entry map[string]any) (surface.SessionSearchResult, bool) {
	thread, _ := entry["thread"].(map[string]any)
	if thread == nil {
		thread = entry
	}
	session := r.session(thread)
	if session.ID == "" {
		return surface.SessionSearchResult{}, false
	}
	return surface.SessionSearchResult{Session: session, Snippet: transcript.String(entry, "snippet")}, true
}
