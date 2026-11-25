package daemon

import "testing"

func TestAnAgentRenamesAPhaseAndMarksVerifyAndReadsBothBack(t *testing.T) {
	d, _, _, _, _, _ := questionDaemon(t)
	server := mcpServer(t, d)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))
	write := func(id, op string, request map[string]any) map[string]any {
		t.Helper()
		out, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": op, "clientRequestId": id, "request": request})
		if refused {
			t.Fatalf("%s %v refused: %v", op, request, out)
		}
		return out
	}
	write("add", "work", map[string]any{"action": "add", "text": "import the March export"})
	write("claim", "work", map[string]any{"action": "claim", "itemId": "widget-W1"})
	write("verify", "work", map[string]any{"action": "log", "itemId": "widget-W1", "logType": "verify", "text": "running it against a copy of the real data"})
	if out := write("labels", "phases", map[string]any{"labels": map[string]any{"verify": "QA"}}); out["phaseLabels"].(map[string]any)["verify"] != "QA" {
		t.Fatalf("phases = %v, want verify named QA", out)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "phases", "clientRequestId": "bad", "request": map[string]any{"labels": map[string]any{"qa": "QA"}}}); !refused {
		t.Fatalf("an unknown phase key was accepted: %v", out)
	}

	read, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "item", "id": "widget-W1"})
	if refused {
		t.Fatalf("item read refused: %v", read)
	}
	item, _ := read["item"].(map[string]any)
	work, _ := item["item"].(map[string]any)
	labels, _ := read["phaseLabels"].(map[string]any)
	if work["phase"] != "verify" || labels["verify"] != "QA" || labels["build"] != "Build" {
		t.Fatalf("item read = %v, want phase verify with verify named QA and build keeping its name", read)
	}
}
