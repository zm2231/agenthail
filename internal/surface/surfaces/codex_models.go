package surfaces

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if name == "" {
		client, err := c.openReader(ctx, sess)
		if err != nil {
			return "", err
		}
		defer client.Close()
		return codexThreadModel(ctx, client, sess.ID)
	}
	change, err := c.SetModel(ctx, sess, name)
	if err != nil {
		return "", err
	}
	return change.Text(), nil
}

func (c *Codex) SetModel(ctx context.Context, sess *surface.Session, name string) (surface.ModelChange, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	if err := c.loadForDirectInput(ctx, client, sess, name); err != nil {
		if surface.IsDeliveryTerminal(err) {
			return surface.ModelChange{}, err
		}
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	current, err := codexThreadModel(ctx, client, sess.ID)
	if err != nil {
		return surface.ModelChange{}, err
	}
	if current == name {
		if err := clearCodexPendingModel(sess.ID); err != nil {
			return surface.ModelChange{}, fmt.Errorf("clear the pending model: %w", err)
		}
		return surface.ModelChange{Model: current}, nil
	}
	if err := saveCodexPendingModel(sess.ID, codexPendingModel{Model: name, From: current, SetAt: time.Now().UTC()}); err != nil {
		return surface.ModelChange{}, fmt.Errorf("record the pending model: %w", err)
	}
	return surface.ModelChange{Model: current, PendingModel: name, Limit: "A turn started from Codex Desktop's composer or its own queue runs on the model Desktop shows."}, nil
}

func codexThreadModel(ctx context.Context, client codexClient, threadID string) (string, error) {
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return "", err
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	model := str(thread, "model")
	if model == "" {
		model = str(result, "model")
	}
	if model == "" {
		return "", fmt.Errorf("session metadata does not include the active model")
	}
	return model, nil
}

func (c *Codex) Models(ctx context.Context) ([]surface.ModelOption, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, runtimes.unavailable()
	}
	return listCodexModels(ctx, clients[0])
}

func listCodexModels(ctx context.Context, conn codexClient) ([]surface.ModelOption, error) {
	var models []surface.ModelOption
	seen := make(map[string]struct{})
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{"includeHidden": false, "limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := conn.Request(ctx, "model/list", params, 10*time.Second)
		if err != nil {
			return nil, err
		}
		result, _ := response["result"].(map[string]any)
		data, _ := result["data"].([]any)
		for _, raw := range data {
			value, _ := raw.(map[string]any)
			id := str(value, "model")
			if id == "" {
				id = str(value, "id")
			}
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			displayName := str(value, "displayName")
			if displayName == "" {
				displayName = id
			}
			models = append(models, surface.ModelOption{
				ID:                        id,
				DisplayName:               displayName,
				Description:               str(value, "description"),
				Default:                   value["isDefault"] == true,
				SupportedReasoningEfforts: stringList(value["supportedReasoningEfforts"], "reasoningEffort"),
				DefaultReasoningEffort:    str(value, "defaultReasoningEffort"),
				ServiceTiers:              stringList(value["serviceTiers"], "id"),
			})
		}
		nextCursor, _ := result["nextCursor"].(string)
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}
	return models, nil
}

func stringList(value any, objectKey string) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		switch item := item.(type) {
		case string:
			if item != "" {
				result = append(result, item)
			}
		case map[string]any:
			if item, ok := item[objectKey].(string); ok && item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}
