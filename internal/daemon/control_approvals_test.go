package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f approvalFixture) outsider(t *testing.T) surface.Session {
	t.Helper()
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	outsider := surface.Session{ID: "outsider", Name: "Gadget release", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: gadget, LastActive: time.Now()}
	f.d.Surfaces[0].(*daemonSurface).sessions[outsider.ID] = outsider
	if err := f.r.RecordObservedSession(outsider); err != nil {
		t.Fatal(err)
	}
	f.d.discoverCatalog(context.Background())
	return outsider
}

func (f approvalFixture) workerCaller(t *testing.T) *mcpCaller {
	t.Helper()
	caller, err := f.d.scopeCaller(f.worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	return caller
}

func TestAnAgentAsksThePersonToMessageAnotherProjectsSessionAndTheMessageIsItsOwn(t *testing.T) {
	f := newApprovalFixture(t)
	setAgentDecision(t, "cross-project-message", dashconfig.AgentAsk)
	outsider := f.outsider(t)
	caller := f.workerCaller(t)
	send := mcpSendInput{mcpWrite: mcpWrite{ClientRequestID: "cross-1"}, Target: outsider.ID, Message: "the gadget API changed; rebuild against v2"}

	_, err := f.d.mcpSend(context.Background(), caller, send)
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || refusal.Detail["approvable"] != true || !strings.Contains(refusal.Message, "askPerson") {
		t.Fatalf("the cross-project send was answered %v, want an approvable refusal that says how to ask", err)
	}
	if rows, _ := f.r.RowAsks(); rows[f.worker.ID].ID != "" {
		t.Fatalf("a refusal without askPerson raised %+v", rows[f.worker.ID])
	}

	send.AskPerson, send.Reason = true, "their build breaks without it"
	asked, err := f.d.mcpSend(context.Background(), caller, send)
	reply, _ := asked.(map[string]any)
	askID, _ := reply["requestId"].(string)
	if err != nil || askID == "" {
		t.Fatalf("asking answered %+v (%v)", asked, err)
	}
	ask, _, err := f.r.Ask(askID)
	if err != nil || !strings.Contains(ask.Title, `Send Gadget release this message: "the gadget API changed`) || strings.Contains(ask.Title, outsider.ID) {
		t.Fatalf("the ask is titled %q (%v), want the send and the target's row name", ask.Title, err)
	}

	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusOK {
		t.Fatalf("allowing it answered %d %s", reply.Status, reply.Body)
	}
	history, err := f.r.ListHistory(50, outsider.ID)
	if err != nil {
		t.Fatal(err)
	}
	var delivered []registry.HistoryEntry
	for _, entry := range history {
		if strings.Contains(entry.Message, "rebuild against v2") {
			delivered = append(delivered, entry)
		}
	}
	if len(delivered) != 1 || delivered[0].SourceSessionID != f.worker.ID {
		t.Fatalf("the allowed message was recorded as %+v, want once, from the agent that asked", delivered)
	}
	if got := noticesTo(t, f.r, f.worker.ID, "The person allowed: Send Gadget release this message"); got != 1 {
		t.Fatalf("the agent was told %d times, want once", got)
	}
}

func TestAnAgentAsksThePersonToArchiveAnotherProjectsSession(t *testing.T) {
	f := newApprovalFixture(t)
	outsider := f.outsider(t)
	asked, err := f.d.mcpArchive(context.Background(), f.workerCaller(t), mcpArchiveInput{mcpWrite: mcpWrite{ClientRequestID: "archive-1"}, Target: outsider.ID, mcpAskPerson: mcpAskPerson{AskPerson: true, Reason: "it finished"}})
	reply, _ := asked.(map[string]any)
	askID, _ := reply["requestId"].(string)
	if err != nil || askID == "" {
		t.Fatalf("asking answered %+v (%v)", asked, err)
	}
	if archived, _ := f.r.SessionArchived(outsider.ID); archived {
		t.Fatal("the session was archived before the person answered")
	}
	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusOK {
		t.Fatalf("allowing it answered %d %s", reply.Status, reply.Body)
	}
	if archived, err := f.r.SessionArchived(outsider.ID); err != nil || !archived {
		t.Fatalf("the allowed archive did not run: %v %v", archived, err)
	}
}

func TestOnlyACrossProjectRefusalCanBeAskedFor(t *testing.T) {
	f := newApprovalFixture(t)
	_, err := f.d.mcpInterrupt(context.Background(), f.workerCaller(t), mcpInterruptInput{mcpWrite: mcpWrite{ClientRequestID: "up-1"}, Target: f.lead.ID, mcpAskPerson: mcpAskPerson{AskPerson: true, Reason: "it is stuck"}})
	var refusal *mcpRefusal
	if !errors.As(err, &refusal) || refusal.Detail["rule"] != "down_only" || refusal.Detail["approvable"] != nil {
		t.Fatalf("interrupting the coordinator answered %v, want the down-only refusal, not approvable", err)
	}
	if rows, _ := f.r.RowAsks(); rows[f.worker.ID].ID != "" {
		t.Fatalf("a refusal that is not cross-project raised %+v", rows[f.worker.ID])
	}
}

func TestTheCLIAsksWithTheBodyTheDaemonRebuildsNotTheOneItSent(t *testing.T) {
	setAgentDecision(t, "cross-project-message", dashconfig.AgentAsk)
	setAgentDecision(t, "cross-project-steer", dashconfig.AgentAsk)
	f := newApprovalFixture(t)
	outsider := f.outsider(t)
	crafted := `{"action":"send","sessionId":"outsider","message":"ship it","noQueue":true,"sourceSessionId":"to","author":"person","scopeProjectId":"elsewhere","relayHops":2}`
	refused := askScope(t, f.d, ScopeRequest{SessionID: f.worker.ID, Action: "send", Target: outsider.ID, Payload: scopePayload(PersonActionAPI, crafted)}, "127.0.0.1:50000")
	if refused.status != http.StatusForbidden || !strings.Contains(string(refused.body), "--ask-person") || !strings.Contains(string(refused.body), `"approvable":true`) {
		t.Fatalf("the CLI's cross-project send was answered %d %s, want an approvable refusal that says how to ask", refused.status, refused.body)
	}
	asked := askScope(t, f.d, ScopeRequest{SessionID: f.worker.ID, Action: "send", Target: outsider.ID, Payload: scopePayload(PersonActionAPI, crafted), AskPerson: true, Reason: "release"}, "127.0.0.1:50000")
	if asked.status != http.StatusOK || asked.reply.AskID == "" {
		t.Fatalf("asking answered %d %s", asked.status, asked.body)
	}
	approval, _, err := f.r.Approval(asked.reply.AskID)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(approval.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"action": "send", "sessionId": outsider.ID, "message": "ship it", "sourceSessionId": f.worker.ID, "busyDelivery": "queue", "noQueue": true}
	for key, value := range want {
		if stored[key] != value {
			t.Fatalf("the stored body is %v, want %s %v", stored, key, value)
		}
	}
	for key, empty := range map[string]any{"author": "", "scopeProjectId": "", "relayHops": float64(0)} {
		if stored[key] != empty {
			t.Fatalf("the stored body kept %s %v from the CLI", key, stored[key])
		}
	}
}
