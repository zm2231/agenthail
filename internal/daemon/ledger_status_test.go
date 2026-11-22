package daemon

import (
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestTheLedgerStatusViewIsTheCallersOwnUnlessItNamesAnother(t *testing.T) {
	d, r, handler, _, _, widget := questionDaemon(t)
	widget.session(t, r, "s2")
	for _, req := range []ledger.WorkRequest{
		{SessionID: "s1", Action: ledger.WorkAdd, Text: "add the index"},
		{SessionID: "s1", Action: ledger.WorkAdd, Text: "wire the endpoint"},
		{SessionID: "s1", Action: ledger.WorkClaim, ItemID: "widget-W1"},
		{SessionID: "s2", Action: ledger.WorkClaim, ItemID: "widget-W2"},
		{SessionID: "s2", Action: ledger.WorkSubmit, ItemID: "widget-W2"},
	} {
		req.ProjectKey = widget.key
		if code, body := ledgerWrite(t, handler, "/api/v1/ledger/work", req, nil); code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", req.Action, req.ItemID, code, body)
		}
	}
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	ids := func(status map[string]any, key string) []string {
		var out []string
		rows, _ := status[key].([]any)
		for _, row := range rows {
			out = append(out, row.(map[string]any)["id"].(string))
		}
		return out
	}
	own, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "status"})
	status, _ := own["status"].(map[string]any)
	if refused || status["sessionId"] != "s1" || len(ids(status, "holding")) != 1 || ids(status, "holding")[0] != "widget-W1" || ids(status, "reviewable")[0] != "widget-W2" {
		t.Fatalf("own status = %v", own)
	}
	theirs, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "status", "id": "s2"})
	status, _ = theirs["status"].(map[string]any)
	if refused || status["sessionId"] != "s2" || len(ids(status, "inReview")) != 1 || len(ids(status, "holding")) != 0 {
		t.Fatalf("another session's status = %v", theirs)
	}
}
