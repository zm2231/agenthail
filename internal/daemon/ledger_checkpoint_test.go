package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func checkpoint(t *testing.T, f gateFixture) string {
	t.Helper()
	var out ledgerCheckpointResponse
	if code, body := ledgerWrite(t, f.d.dashboardHandler(&dashboardServer{token: "test-token"}), "/api/v1/ledger/checkpoint", ledgerCheckpointRequest{SessionID: f.session.ID}, &out); code != 200 {
		t.Fatalf("checkpoint %d %s", code, body)
	}
	return out.Instruction
}

func TestAProjectThatAttachesAtCheckpointsAsksNothingPerMessage(t *testing.T) {
	f := newGateFixture(t, registry.OriginAgent, surface.RuntimeClaudeHost)
	if err := f.project.store.Enable(f.project.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	handler := f.d.dashboardHandler(&dashboardServer{token: "test-token"})
	earlier := ledger.Utterance{ProjectKey: f.project.key, Ref: "claude-prompt:2", SpeakerID: surface.PersonSessionID, TargetID: f.session.ID, Text: "add search to the docs site", At: time.Now().Add(-time.Hour), CapturedBy: ledger.CapturedByHook}
	if _, _, err := f.project.store.RecordUtterance(earlier); err != nil {
		t.Fatal(err)
	}
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 || f.r.QueueCount(f.session.ID) != 0 {
		t.Fatalf("G2 held the turn for unattached words: %q", f.fake.sent)
	}
	if text := checkpoint(t, f); text != ledger.ProvisionalNote(f.project.key, 1) {
		t.Fatalf("checkpoint = %q", text)
	}
	f.ownClause(t, time.Now().Add(-time.Hour))
	f.completeTurn(t, "turn-2")
	if all := strings.Join(f.fake.sent, "\n"); !strings.Contains(all, "widget-C1: build search") || strings.Contains(all, "claude-prompt:2") {
		t.Fatalf("G2 should still hold for a clause without progress, and only for it: %q", f.fake.sent)
	}

	var batch ledger.AttachEachResult
	code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", map[string]any{"projectKey": f.project.key, "sessionId": f.session.ID, "fragments": []ledger.AttachRequest{
		{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A1", Clauses: []string{"search in the docs site"}},
		{UtteranceID: "claude-prompt:2", As: ledger.AsExtends, To: "widget-A9", Clauses: []string{"and the blog"}},
	}}, &batch)
	if code != 200 || len(batch.Fragments) != 2 || batch.Fragments[0].Refused != "" || !strings.Contains(batch.Fragments[0].Readback, "widget-A1") || batch.Fragments[1].Refused == "" {
		t.Fatalf("batch attach %d %s: %+v", code, body, batch)
	}
	if text := checkpoint(t, f); text != "" {
		t.Fatalf("attached words still shown at the checkpoint: %q", text)
	}
	before := len(f.project.events(t))
	for field, value := range map[string]any{"as": "extends", "utteranceId": "claude-prompt:2", "askType": "decision", "source": "proposed", "adopt": true, "reject": true, "ack": true, "delegate": "worker", "covers": []string{"widget-T1"}, "clauses": []string{"x"}, "to": "project"} {
		request := map[string]any{"projectKey": f.project.key, "sessionId": f.session.ID, field: value, "fragments": []ledger.AttachRequest{{UtteranceID: "claude-prompt:2", As: ledger.AsConversation}}}
		if code, _ := ledgerWrite(t, handler, "/api/v1/ledger/attach", request, nil); code != 400 {
			t.Errorf("%s beside fragments: %d, want refused", field, code)
		}
	}
	if after := len(f.project.events(t)); after != before {
		t.Fatalf("a refused batch wrote %d events", after-before)
	}

	if got := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: f.session.ID, PromptID: "p-1", Prompt: "and the changelog"}); !got.Captured || got.Attach || got.Provisional != 0 {
		t.Fatalf("a message in checkpoint mode: %+v", got)
	}
	if err := f.project.store.Enable(f.project.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if got := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: f.session.ID, PromptID: "p-2", Prompt: "and the readme"}); !got.Attach {
		t.Fatalf("back to attaching each message: %+v", got)
	}
	if text := checkpoint(t, f); text != "" {
		t.Fatalf("checkpoints shown in prompt mode: %q", text)
	}
}

func TestASessionGrantedIntoACheckpointProjectSeesItsWordsThere(t *testing.T) {
	f := newGateFixture(t, registry.OriginAgent, surface.RuntimeClaudeHost)
	other := addLedgerProject(t, f.r, "proj-other", "repo:github.com/acme/infra")
	if err := other.store.Enable(other.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	if err := other.store.GrantSession(other.key, f.session.ID); err != nil {
		t.Fatal(err)
	}
	u := ledger.Utterance{ProjectKey: other.key, Ref: "claude-prompt:9", SpeakerID: surface.PersonSessionID, TargetID: f.session.ID, Text: "rotate keys", At: time.Now().Add(-time.Hour), CapturedBy: ledger.CapturedByHook}
	if _, _, err := other.store.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	if text := checkpoint(t, f); text != ledger.ProvisionalNote(other.key, 1) {
		t.Fatalf("checkpoint for a granted project = %q", text)
	}
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 {
		t.Fatalf("G2 held a granted session for words in a checkpoint project: %q", f.fake.sent)
	}
}

func TestMCPWorkWritesInCheckpointModeCarryTheProvisionalCount(t *testing.T) {
	d, _, handler, _, _, widget := questionDaemon(t)
	if err := widget.store.Enable(widget.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: "s1", PromptID: "p-1", Prompt: "rename the flag"})
	server := mcpServer(t, d)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))
	add, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "w1", "request": map[string]any{"action": "add", "text": "rename the flag"}})
	if text, _ := add["ledgerCheckpoint"].(string); refused || text != ledger.ProvisionalNote(widget.key, 1) || add["readback"] == nil {
		t.Fatalf("work add over MCP: %v", add)
	}
	comment, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "w2", "request": map[string]any{"action": "comment", "itemId": "widget-W1", "text": "started"}})
	if _, shown := comment["ledgerCheckpoint"]; refused || shown {
		t.Fatalf("a comment is not a checkpoint: %v", comment)
	}
}

func TestALeadInAProjectThatAttachesAtCheckpointsIsAskedAtCheckpointsOnly(t *testing.T) {
	f := gateFixtureIn(t, registry.OriginCoordinator, surface.RuntimeClaudeHost, false)
	makeCoordinator(t, f.r, f.project, f.session.ID)
	if err := f.project.store.Enable(f.project.key, ledger.AttachAtCheckpoints); err != nil {
		t.Fatal(err)
	}
	if got := ledgerPost(t, f.d.dashboardHandler(&dashboardServer{token: "test-token"}), ledgerCaptureRequest{SessionID: f.session.ID, PromptID: "p-0", Prompt: "and the docs"}); !got.Captured || got.Attach {
		t.Fatalf("the lead was asked per message in a project that attaches at checkpoints: %+v", got)
	}
	if text := checkpoint(t, f); text != ledger.ProvisionalNote(f.project.key, 1) {
		t.Fatalf("checkpoint = %q", text)
	}
	f.completeTurn(t, "turn-1")
	if len(f.fake.sent) != 0 || f.r.QueueCount(f.session.ID) != 0 {
		t.Fatalf("the lead was held for unattached words at checkpoints: %q", f.fake.sent)
	}
}
