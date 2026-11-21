package codexsurface

import (
	"context"
	"testing"
)

func TestCodexModelsPreservesCatalogAndStopsAtEmptyCursor(t *testing.T) {
	pages := map[string]map[string]any{
		"": {
			"data": []any{
				map[string]any{"id": "gpt-5.6-sol", "displayName": "GPT-5.6-Sol", "description": "workhorse", "isDefault": true, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low", "description": "fast"}, map[string]any{"reasoningEffort": "high", "description": "deep"}}, "defaultReasoningEffort": "high", "serviceTiers": []any{map[string]any{"id": "priority", "name": "Fast"}}},
				map[string]any{"model": "chatgpt-web/pro", "displayName": "ChatGPT Web — Pro"},
			},
			"nextCursor": "page-2",
		},
		"page-2": {
			"data": []any{
				map[string]any{"id": "gpt-5.6-sol", "displayName": "duplicate"},
				map[string]any{"id": "gpt-5.5"},
			},
			"nextCursor": "",
		},
	}
	fake := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method != "model/list" {
			return nil
		}
		cursor, _ := params["cursor"].(string)
		return pages[cursor]
	})
	models, err := isolatedManagedRuntime(t).Models(context.Background())
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
	if calls := fake.Calls("model/list"); len(calls) != 2 {
		t.Fatalf("requested %d pages after empty cursor", len(calls))
	}
}
