package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexExecLine(t *testing.T, source string) string {
	t.Helper()
	record := map[string]any{"timestamp": "2026-10-22T20:08:00Z", "type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "status": "completed", "call_id": "call_1", "name": "exec", "input": source}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestCodexPlanReadsUpdatePlanFromCodeModeAndFunctionCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	first := codexExecLine(t, "const p = await tools.update_plan({plan:[\n  {step:\"Inspect the diff\",status:\"in_progress\"},\n  {step:'Trace callers',status:\"pending\"},\n  {step:\"Run gates\",status:\"pending\"},\n]});\ntext(p)")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{}}`+"\n"+first), 0600); err != nil {
		t.Fatal(err)
	}
	codex := NewCodex("")
	read := func() *surface.Plan {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return codex.plan(context.Background(), path, info)
	}
	if plan := read(); plan == nil || *plan != (surface.Plan{Done: 0, Total: 3, Current: "Inspect the diff"}) {
		t.Fatalf("code-mode plan=%+v", plan)
	}

	appendTranscript(t, path, codexExecLine(t, `const p = await tools.update_plan({explanation:"Traced, gates next.",plan:[{step:"Inspect the diff",status:"completed"},{step:"Trace callers",status:"completed"},{step:"Run gates",status:"in_progress"}]});`))
	if plan := read(); plan == nil || *plan != (surface.Plan{Done: 2, Total: 3, Current: "Run gates"}) {
		t.Fatalf("after the appended update plan=%+v", plan)
	}

	appendTranscript(t, path, codexExecLine(t, `const steps = build(); await tools.update_plan({plan: steps});`))
	if plan := read(); plan != nil {
		t.Fatalf("a plan built from a variable can't be read, so none is shown rather than the old one: %+v", plan)
	}

	appendTranscript(t, path, `{"type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{\"plan\":[{\"step\":\"Ship\",\"status\":\"completed\"}]}"}}`+"\n")
	if plan := read(); plan == nil || *plan != (surface.Plan{Done: 1, Total: 1}) {
		t.Fatalf("function-call plan=%+v", plan)
	}
}

func TestJSLiteralJSONAcceptsOnlyPlainLiterals(t *testing.T) {
	for _, literal := range []string{`{plan: steps}`, "{plan:[{step:`x`}]}", `{plan:[{step:"a" + b}]}`} {
		if converted, ok := jsLiteralJSON(literal); ok && json.Valid([]byte(converted)) {
			t.Fatalf("%s converted to %s", literal, converted)
		}
	}
	converted, ok := jsLiteralJSON(`{ok:true, n: 2, step:'it\'s "fine"', list:[1,2,],}`)
	var value map[string]any
	if !ok || json.Unmarshal([]byte(converted), &value) != nil || value["step"] != `it's "fine"` || value["ok"] != true {
		t.Fatalf("converted=%s ok=%v value=%v", converted, ok, value)
	}
}

func TestCodexPlanForgetsRolloutsNoListingReads(t *testing.T) {
	dir := t.TempDir()
	codex := NewCodex("")
	paths := []string{filepath.Join(dir, "kept.jsonl"), filepath.Join(dir, "gone.jsonl")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(codexExecLine(t, `await tools.update_plan({plan:[{step:"Ship",status:"pending"}]});`)), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if plan := codex.plan(context.Background(), path, info); plan == nil {
			t.Fatalf("no plan for %s", path)
		}
	}
	codex.forgetUnreadPlans()
	if len(codex.planState) != 2 {
		t.Fatalf("a listing that read both rollouts dropped a plan: %d left", len(codex.planState))
	}
	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	codex.plan(context.Background(), paths[0], info)
	codex.forgetUnreadPlans()
	if _, kept := codex.planState[paths[0]]; !kept || len(codex.planState) != 1 {
		t.Fatalf("after a listing that read one rollout the cache holds %d plans", len(codex.planState))
	}
}
