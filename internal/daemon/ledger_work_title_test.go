package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestAWorkTitleOverEightyCharactersIsRefusedOverHTTPAndMCP(t *testing.T) {
	d, _, handler, _, _, widget := questionDaemon(t)
	fits, other, long := strings.Repeat("é", 80), strings.Repeat("ü", 80), strings.Repeat("é", 81)
	const advice = "put the rest in the description"

	post := func(req ledger.WorkRequest) (int, string) {
		req.ProjectKey, req.SessionID = widget.key, "s1"
		return ledgerWrite(t, handler, "/api/v1/ledger/work", req, nil)
	}
	if code, body := post(ledger.WorkRequest{Action: ledger.WorkAdd, Text: long}); code == http.StatusOK || !strings.Contains(body, advice) {
		t.Fatalf("HTTP add of 81 characters = %d %s", code, body)
	}
	if code, body := post(ledger.WorkRequest{Action: ledger.WorkAdd, Text: fits}); code != http.StatusOK {
		t.Fatalf("HTTP add of 80 characters = %d %s", code, body)
	}
	if code, body := post(ledger.WorkRequest{Action: ledger.WorkEdit, ItemID: "widget-W1", Change: ledger.WorkChange{Text: &long}}); code == http.StatusOK || !strings.Contains(body, advice) {
		t.Fatalf("HTTP edit to 81 characters = %d %s", code, body)
	}

	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	write := func(id string, request map[string]any) (map[string]any, bool) {
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": id, "request": request})
	}
	for id, request := range map[string]map[string]any{
		"add-long":  {"action": "add", "text": long},
		"edit-long": {"action": "edit", "itemId": "widget-W1", "change": map[string]any{"text": long}},
	} {
		if out, refused := write(id, request); !refused || !strings.Contains(fmt.Sprint(out["message"]), advice) {
			t.Fatalf("MCP %s = %v, want a refusal saying to %s", id, out, advice)
		}
	}
	if out, refused := write("edit-fits", map[string]any{"action": "edit", "itemId": "widget-W1", "change": map[string]any{"text": other}}); refused {
		t.Fatalf("MCP edit to 80 characters = %v", out)
	}
}
