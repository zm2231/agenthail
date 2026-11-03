package daemon

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestMCPLedgerReadIsTheCallersProjectBoard(t *testing.T) {
	d, r, handler, _, _, widget := questionDaemon(t)
	ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widget.key, SessionID: "s1", Name: "Search"}, nil)
	ask(t, handler, widget, ledger.QuestionRequest{To: "widget-G1", Text: "which index?", DefaultAnswer: "full-text", Kind: ledger.QuestionScope})
	other := addLedgerProject(t, r, "proj-other", "repo:github.com/acme/other")
	server := mcpServer(t, d)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))
	board, refused := callMCP(t, session, "agenthail_ledger_read", nil)
	counts, _ := board["counts"].(map[string]any)
	goals, _ := board["goals"].([]any)
	if refused || board["projectKey"] != widget.key || counts["goals"] != 1.0 || counts["questions"] != 1.0 || len(goals) != 1 {
		t.Fatalf("board=%v", board)
	}
	questions, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "questions"})
	if list, _ := questions["questions"].([]any); refused || len(list) != 1 {
		t.Fatalf("questions=%v", questions)
	}
	if refusal, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "owner"}); !refused || refusal["code"] != "invalid" {
		t.Fatalf("unknown view=%v", refusal)
	}
	outsider := mcpSession(t, server, issueMCPToken(t, d, other.session(t, r, "outsider")))
	if board, refused := callMCP(t, outsider, "agenthail_ledger_read", nil); refused || board["projectKey"] != other.key || board["counts"].(map[string]any)["goals"] != 0.0 {
		t.Fatalf("other board=%v", board)
	}
	loose, _, _, from, _ := daemonFixture(t)
	if refusal, refused := callMCP(t, mcpSession(t, mcpServer(t, loose), issueMCPToken(t, loose, from)), "agenthail_ledger_read", nil); !refused || refusal["code"] != "invalid" {
		t.Fatalf("no project=%v", refusal)
	}
}

func TestMCPRequestsListsOnlyVisibleWaitingSessions(t *testing.T) {
	d, r, _, _, _, widget := questionDaemon(t)
	mate := widget.session(t, r, "mate")
	other := addLedgerProject(t, r, "proj-other", "repo:github.com/acme/other")
	stranger := other.session(t, r, "stranger")
	now := time.Now()
	for _, id := range []string{mate.ID, stranger.ID} {
		if _, err := r.RecordWaiting(id, &surface.Waiting{Kind: surface.WaitingApproval, ID: "req-" + id, Since: now, Answerable: true}, now); err != nil {
			t.Fatal(err)
		}
	}
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	listing, refused := callMCP(t, session, "agenthail_requests", nil)
	waiting, _ := listing["waiting"].([]any)
	if refused || len(waiting) != 1 || waiting[0].(map[string]any)["sessionId"] != mate.ID || waiting[0].(map[string]any)["requestId"] != "req-mate" {
		t.Fatalf("waiting=%v", listing)
	}
	if refusal, refused := callMCP(t, session, "agenthail_requests", map[string]any{"sessionId": stranger.ID}); !refused || refusal["code"] != "out_of_scope" {
		t.Fatalf("stranger=%v", refusal)
	}
}

func TestMCPCapabilitiesNameTheReadReachAndLimits(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	result, refused := callMCP(t, mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from)), "agenthail_capabilities", nil)
	limits, _ := result["limits"].(map[string]any)
	reads, _ := result["reads"].([]any)
	if refused || limits["outputBytes"] != float64(mcpOutputLimit) || len(reads) != 3 {
		t.Fatalf("capabilities=%v", result)
	}
}

func TestEveryActionErrorMapsToAnMCPCode(t *testing.T) {
	raw, err := os.ReadFile("testdata/action_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases map[string]struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for name, recorded := range cases {
		var body struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(recorded.Body), &body) != nil || body.Error == nil {
			continue
		}
		checked++
		if _, known := mcpCodeFor(recorded.Status, body.Error.Code); !known {
			t.Errorf("%s: %d %s has no MCP mapping", name, recorded.Status, body.Error.Code)
		}
	}
	if checked == 0 {
		t.Fatal("the parity table recorded no action errors")
	}
	for _, tc := range []struct {
		status          int
		code, want      string
		retryable, gone bool
	}{
		{409, "stale", "not_found", false, true},
		{409, "move_busy", "busy", true, false},
		{502, "control_failed", "busy", true, false},
		{502, "create_unknown", "busy", false, false},
		{409, "move_refused", "refused", false, false},
	} {
		mapped, _ := mcpCodeFor(tc.status, tc.code)
		if mapped.Code != tc.want || mapped.Retryable != tc.retryable || (mapped.Detail["stale"] == true) != tc.gone {
			t.Errorf("%d %s => %+v", tc.status, tc.code, mapped)
		}
	}
}
