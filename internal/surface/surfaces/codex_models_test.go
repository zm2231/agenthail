package surfaces

import (
	"context"
	"testing"
	"time"
)

type modelListClient struct {
	pages []map[string]any
	index int
}

func (c *modelListClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	if method != "model/list" {
		return nil, nil
	}
	if c.index > 0 && params["cursor"] != "page-2" {
		return map[string]any{"result": map[string]any{"data": []any{}, "nextCursor": nil}}, nil
	}
	page := c.pages[c.index]
	c.index++
	return page, nil
}

func (c *modelListClient) Close() error { return nil }

func TestListCodexModelsPreservesCatalogAndStopsAtEmptyCursor(t *testing.T) {
	client := &modelListClient{pages: []map[string]any{
		{"result": map[string]any{
			"data": []any{
				map[string]any{"id": "gpt-5.6-sol", "displayName": "GPT-5.6-Sol", "description": "workhorse", "isDefault": true, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low", "description": "fast"}, map[string]any{"reasoningEffort": "high", "description": "deep"}}, "defaultReasoningEffort": "high", "serviceTiers": []any{map[string]any{"id": "priority", "name": "Fast"}}},
				map[string]any{"model": "chatgpt-web/pro", "displayName": "ChatGPT Web — Pro"},
			},
			"nextCursor": "page-2",
		}},
		{"result": map[string]any{
			"data": []any{
				map[string]any{"id": "gpt-5.6-sol", "displayName": "duplicate"},
				map[string]any{"id": "gpt-5.5"},
			},
			"nextCursor": "",
		}},
	}}

	models, err := listCodexModels(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models: %#v", len(models), models)
	}
	if models[0].ID != "gpt-5.6-sol" || !models[0].Default || models[0].Description != "workhorse" {
		t.Fatalf("first model lost catalog metadata: %#v", models[0])
	}
	if models[0].DefaultReasoningEffort != "high" || len(models[0].SupportedReasoningEfforts) != 2 || models[0].SupportedReasoningEfforts[1] != "high" || len(models[0].ServiceTiers) != 1 || models[0].ServiceTiers[0] != "priority" {
		t.Fatalf("first model capability metadata lost: %#v", models[0])
	}
	if models[1].ID != "chatgpt-web/pro" || models[1].DisplayName != "ChatGPT Web — Pro" {
		t.Fatalf("provider model missing: %#v", models[1])
	}
	if models[2].DisplayName != "gpt-5.5" {
		t.Fatalf("missing display name was not made truthful: %#v", models[2])
	}
	if client.index != 2 {
		t.Fatalf("requested %d pages after empty cursor", client.index)
	}
}

func TestParseClaudeRuntimeModelsPreservesCatalogMetadata(t *testing.T) {
	data := []byte(`{"type":"control_response","response":{"request_id":"catalog","response":{"models":[{"value":"default","displayName":"Default","description":"recommended","supportedEffortLevels":["low","high"]},{"value":"haiku","displayName":"Haiku","resolvedModel":"claude-haiku-4-5-20251001"}]}}}`)
	models, err := parseClaudeRuntimeModels(data, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "default" || !models[0].Default || models[0].Description != "recommended" {
		t.Fatalf("catalog options lost: %#v", models)
	}
	if len(models[0].SupportedReasoningEfforts) != 2 || !models[0].AllowsCustom {
		t.Fatalf("catalog capability metadata lost: %#v", models[0])
	}
}
