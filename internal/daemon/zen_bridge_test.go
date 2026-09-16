package daemon

import (
	"encoding/json"
	"testing"
)

func TestZenStreamEventPreservesNormalizedRuntimeVocabulary(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		kind string
	}{
		{name: "message", raw: `{"type":"message","role":"assistant","text":"hello","id":"m1"}`, kind: "message"},
		{name: "reasoning", raw: `{"type":"thought","text":"considering","id":"r1"}`, kind: "thought"},
		{name: "tool", raw: `{"type":"tool_start","id":"t1","name":"shell","input":{"cmd":"pwd"}}`, kind: "tool_start"},
		{name: "tool result", raw: `{"type":"tool_done","id":"t1","name":"shell","output":"/tmp"}`, kind: "tool_done"},
		{name: "usage", raw: `{"type":"usage","input":10,"output":5,"total":15}`, kind: "usage"},
		{name: "result", raw: `{"type":"done","id":"d1","reason":"completed"}`, kind: "done"},
		{name: "error", raw: `{"type":"error","message":"failed"}`, kind: "error"},
		{name: "turn end", raw: `{"type":"turn_end","turnId":"turn-1","reason":"completed","usage":{"input":10,"output":5,"total":15}}`, kind: "turn_end"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event, err := zenStreamEvent(json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind != test.kind || event.Data == nil {
				t.Fatalf("event=%+v", event)
			}
		})
	}
}

func TestZenStreamEventUsageMapsContext(t *testing.T) {
	event, err := zenStreamEvent(json.RawMessage(`{"type":"usage","input":10,"output":5,"total":15}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.Context == nil || event.Context.InputTokens != 10 || event.Context.OutputTokens != 5 || event.Context.CumulativeTokens != 15 {
		t.Fatalf("context=%+v", event.Context)
	}
}
