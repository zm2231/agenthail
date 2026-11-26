package daemon

import "testing"

func TestAnAgentRecordsProofOverMCPAndARetryRecordsItOnce(t *testing.T) {
	d, _, _, _, _, _ := questionDaemon(t)
	server := mcpServer(t, d)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))
	write := func(id string, request map[string]any) (map[string]any, bool) {
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": id, "request": request})
	}
	if out, refused := write("add", map[string]any{"action": "add", "text": "import the March export"}); refused {
		t.Fatalf("add refused: %v", out)
	}
	if out, refused := write("claim", map[string]any{"action": "claim", "itemId": "widget-W1"}); refused {
		t.Fatalf("claim refused: %v", out)
	}
	step := map[string]any{"action": "proof", "itemId": "widget-W1", "proof": map[string]any{"op": "add", "step": map[string]any{
		"title": "Import the March export", "expected": "every row lands once", "observed": "4,112 rows, none twice", "outcome": "pass",
		"attachments": []any{map[string]any{"title": "run log", "url": "https://ci.example.com/runs/12"}}}}}
	for range 2 {
		if out, refused := write("proof-1", step); refused {
			t.Fatalf("proof refused: %v", out)
		}
	}
	read, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "item", "id": "widget-W1"})
	item, _ := read["item"].(map[string]any)
	work, _ := item["item"].(map[string]any)
	proof, _ := work["proof"].([]any)
	if refused || len(proof) != 1 {
		t.Fatalf("item read = %v, want the one proof step recorded once", read)
	}
	first, _ := proof[0].(map[string]any)
	if first["id"] != "p1" || first["outcome"] != "pass" || first["version"] != float64(1) || first["observed"] != "4,112 rows, none twice" {
		t.Fatalf("proof step = %v", first)
	}
}
