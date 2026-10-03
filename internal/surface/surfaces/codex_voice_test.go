package surfaces

import (
	"context"
	"testing"
	"time"
)

type voiceCreationClient struct {
	methods []string
	params  map[string]any
}

func (c *voiceCreationClient) Close() error { return nil }
func (c *voiceCreationClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.methods = append(c.methods, method)
	c.params = params
	return map[string]any{"result": map[string]any{"thread": map[string]any{"id": "fixture-operator", "path": "/fixture/rollout.jsonl"}}}, nil
}
func TestVoiceOperatorCreatesDurableDesktopThreadWithInstructionsBeforeAnyTurn(t *testing.T) {
	c := &voiceCreationClient{}
	s, err := createVoiceOperator(context.Background(), c, "/fixture/operator", "literal fixture operations skill")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.methods) != 1 || c.methods[0] != "thread/start" || c.params["developerInstructions"] != "literal fixture operations skill" || c.params["ephemeral"] != false {
		t.Fatalf("creation=%+v", c)
	}
	if _, exists := c.params["approvalPolicy"]; exists {
		t.Fatal("operator silently changed native approval policy")
	}
	tools, ok := c.params["dynamicTools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "agenthail" {
		t.Fatalf("dynamic tools=%#v", c.params["dynamicTools"])
	}
	namespaceTools, ok := tools[0]["tools"].([]map[string]any)
	if !ok || len(namespaceTools) != 2 || namespaceTools[0]["name"] != "voice_transfer" || namespaceTools[1]["name"] != "voice_return_to_orchestrator" {
		t.Fatalf("namespace tools=%#v", tools[0]["tools"])
	}
	if s.ID != "fixture-operator" || s.Transport != "desktop" || s.Transcript != "/fixture/rollout.jsonl" {
		t.Fatalf("session=%+v", s)
	}
}
