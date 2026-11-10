package daemon

import (
	"fmt"
	"strings"
	"testing"
)

func TestALedgerWriteWithAMisplacedKeyIsRefusedAndNamesWhereTheKeyGoes(t *testing.T) {
	d, _, _, _, _, _ := questionDaemon(t)
	server := mcpServer(t, d)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))
	write := func(id string, request map[string]any) (map[string]any, bool) {
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": id, "request": request})
	}
	refusals := []struct {
		request map[string]any
		names   []string
	}{
		{map[string]any{"action": "add", "text": "fix the flag", "type": "bug", "priority": "P1"}, []string{`"type" as fields.type`, `"priority" as fields.priority`}},
		{map[string]any{"action": "add", "text": "fix the flag", "fields": map[string]any{"type": "bug", "prio": "P1"}}, []string{`"prio"`}},
		{map[string]any{"action": "edit", "itemId": "widget-W1", "priority": "P0"}, []string{`"priority" as change.priority`}},
	}
	for i, refusal := range refusals {
		out, refused := write(fmt.Sprintf("bad-%d", i), refusal.request)
		message := fmt.Sprint(out["message"])
		for _, name := range refusal.names {
			if !refused || !strings.Contains(message, name) {
				t.Fatalf("request %v: refused=%v out=%v, want a refusal naming %s", refusal.request, refused, out, name)
			}
		}
	}
	out, refused := write("good", map[string]any{"action": "add", "text": "fix the flag", "fields": map[string]any{"type": "bug", "priority": "P1"}})
	item, _ := out["item"].(map[string]any)
	if refused || item["id"] != "widget-W1" || item["type"] != "bug" || item["priority"] != "P1" {
		t.Fatalf("a well-formed add = %v, want widget-W1 as a P1 bug with nothing recorded before it", out)
	}
}
