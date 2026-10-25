package surfaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	session, err := c.resolve(ctx, target)
	if session != nil {
		applyCodexPendingModel(session)
	}
	return session, err
}

func (c *Codex) resolve(ctx context.Context, target string) (*surface.Session, error) {
	if looksLikeUUID(target) {
		return c.resolveID(ctx, target)
	}
	results, err := c.SearchSessions(ctx, target, 20)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	for _, result := range results {
		session := result.Session
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
	return thread, nil
}

func (c *Codex) SearchSessions(ctx context.Context, query string, limit int) ([]surface.SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("Codex history search requires a query")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
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
	var response map[string]any
	var err error
	for _, client := range clients {
		if response, err = client.Request(ctx, "thread/search", params, 5*time.Second); err == nil {
			break
		}
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "method not found") || strings.Contains(err.Error(), "-32601") {
			return nil, surface.ErrUnsupported
		}
		return nil, fmt.Errorf("Codex history search: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	output := make([]surface.SessionSearchResult, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		thread, _ := entry["thread"].(map[string]any)
		if thread == nil {
			thread = entry
		}
		session := runtimes.session(thread)
		if session.ID == "" {
			continue
		}
		output = append(output, surface.SessionSearchResult{Session: session, Snippet: str(entry, "snippet")})
	}
	return output, nil
}
