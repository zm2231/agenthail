package codexsurface

import (
	"context"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

const codexIncludedThreadLimit = 100

var codexEverySourceKind = []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}

// A Codex without includedThreadIds lists recent threads instead, so only asked ids are taken and the rest read one by one.
func (c *Codex) readThreadObjects(ctx context.Context, conn codexClient, ids []string) ([]map[string]any, []error) {
	byID := make(map[string]map[string]any, len(ids))
	for start := 0; start < len(ids); start += codexIncludedThreadLimit {
		chunk := ids[start:min(start+codexIncludedThreadLimit, len(ids))]
		for _, thread := range c.listThreadsByID(ctx, conn, chunk) {
			byID[transcript.String(thread, "id")] = thread
		}
	}
	threads := make([]map[string]any, 0, len(ids))
	var failures []error
	for _, id := range ids {
		if thread := byID[id]; thread != nil {
			threads = append(threads, thread)
			continue
		}
		thread, err := c.readThreadObject(ctx, conn, id)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		threads = append(threads, thread)
	}
	return threads, failures
}

func (c *Codex) listThreadsByID(ctx context.Context, conn codexClient, ids []string) []map[string]any {
	response, err := conn.Request(ctx, "thread/list", map[string]any{
		"includedThreadIds": ids,
		"limit":             len(ids),
		"sourceKinds":       codexEverySourceKind,
		"modelProviders":    []string{},
		"useStateDbOnly":    true,
	}, codexAppServerWait)
	if err != nil {
		return nil
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	attachCodexReadStates(result, values)
	threads := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if thread, _ := value.(map[string]any); thread != nil {
			threads = append(threads, thread)
		}
	}
	return threads
}
