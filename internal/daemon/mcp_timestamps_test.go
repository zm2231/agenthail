package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestMCPResultsUseTheAPITimestampsAndAFullPageStillFits(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	for index := range 600 {
		at := time.Date(2026, 11, 2, 10, 0, index%60, 0, time.UTC).Format(time.RFC3339)
		appendJournalPayloads(t, r, from.ID, sessionJournalPayload{ItemID: fmt.Sprintf("i%03d", index), Kind: "message", Role: "assistant", Body: "ok", TurnID: "t", TS: at})
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))
	result, err := session.CallTool(t.Context(), callParams("agenthail_read", map[string]any{"sessionId": from.ID, "since": 0}))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if result.IsError || len(encoded) > mcpOutputLimit {
		t.Fatalf("a page of small items: %d bytes, isError=%v %s", len(encoded), result.IsError, encoded[:min(len(encoded), 300)])
	}
	var out struct {
		Items     []mcpItemView `json:"items"`
		Truncated bool          `json:"truncated"`
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) < 100 || !out.Truncated {
		t.Fatalf("the page held %d items, truncated=%v; want a full page that continues", len(out.Items), out.Truncated)
	}
	for _, item := range out.Items {
		if _, err := time.Parse("2006-01-02T15:04:05.000000000Z", item.Timestamp); err != nil {
			t.Fatalf("item %s timestamp %q is not the API's UTC RFC 3339 layout", item.ID, item.Timestamp)
		}
	}
}
