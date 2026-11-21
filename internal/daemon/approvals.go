package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) answerApproval(ctx context.Context, ask registry.Ask, response surface.Response, p principal, by string) (actionResult, error) {
	if !answeredByThePerson(p, by) {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person allows an action an agent was refused")
	}
	if len(response.Choices) != 1 || (response.Choices[0] != registry.ApprovalAllow && response.Choices[0] != registry.ApprovalDeny) {
		return actionResult{}, invalidRequest("choose allow or deny")
	}
	choice := response.Choices[0]
	approval, found, err := d.Registry.Approval(ask.ID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !found || len(ask.Questions) != 1 {
		return actionResult{}, staleRequest()
	}
	decided, err := d.Registry.DecideApproval(ask.ID, choice, ask.Questions[0].Question, p.ID, p.actor(), time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !decided {
		return actionResult{}, staleRequest()
	}
	defer d.publishAsk(ask.SessionID)
	d.sendWithdraws()
	if choice == registry.ApprovalDeny {
		d.log.Printf("approval %s: denied by %s: %s", ask.ID, p.actor(), approval.Description)
		d.tellApprovalAsker(approval, "[agenthail] The person denied: "+approval.Description+".", "denied")
		return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "denied"}}), nil
	}
	reply := d.runApproval(ctx, p, approval)
	status, summary := registry.ApprovalRan, approvalSummary(reply)
	if reply.Status < http.StatusOK || reply.Status >= http.StatusMultipleChoices {
		status = registry.ApprovalFailed
	}
	if err := d.Registry.RecordApprovalRun(ask.ID, status, summary, time.Now()); err != nil {
		d.log.Printf("approval %s: record the run: %s", ask.ID, err)
	}
	d.log.Printf("approval %s: allowed by %s, %s: %s (%d)", ask.ID, p.actor(), status, approval.Description, reply.Status)
	message := "[agenthail] The person allowed: " + approval.Description + ". It ran: " + summary
	if status == registry.ApprovalFailed {
		message = "[agenthail] The person allowed: " + approval.Description + ". It failed: " + summary
	}
	d.tellApprovalAsker(approval, message, status)
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": status, "status": reply.Status, "body": json.RawMessage(approvalBody(reply))}}), nil
}

func (d *Daemon) runApproval(ctx context.Context, p principal, approval registry.Approval) actionReply {
	if approval.API == daemonapi.PersonActionSettings {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/settings", bytes.NewReader(approval.Payload))
		if err != nil {
			return actionReply{Status: http.StatusInternalServerError, Body: []byte(err.Error())}
		}
		recorder := newActionRecorder()
		d.dashboardSettingsHandler(recorder, request)
		return recorder.reply()
	}
	return d.performAction(ctx, p, actionFormatAPIv1, approval.Payload, "approval-"+approval.AskID)
}

func approvalBody(reply actionReply) []byte {
	if json.Valid(reply.Body) {
		return reply.Body
	}
	encoded, _ := json.Marshal(strings.TrimSpace(string(reply.Body)))
	return encoded
}

func approvalSummary(reply actionReply) string {
	var answer struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(reply.Body, &answer) == nil && answer.Error.Message != "" {
		return fmt.Sprintf("%d %s: %s", reply.Status, answer.Error.Code, answer.Error.Message)
	}
	return clipText(fmt.Sprintf("%d %s", reply.Status, strings.TrimSpace(string(reply.Body))), 600)
}

func (d *Daemon) tellApprovalAsker(approval registry.Approval, message, outcome string) {
	if _, err := d.sendNotice(noticeRecipient{sessionID: approval.SessionID, role: "asker"}, message, "approval-"+outcome+":"+approval.AskID); err != nil {
		d.log.Printf("approval %s: tell %s: %s", approval.AskID, d.resolveDisplay(approval.SessionID), err)
	}
}

func (d *Daemon) expireApprovals(now time.Time) {
	expired, err := d.Registry.ExpireApprovals(now)
	if err != nil {
		d.log.Printf("approvals: expire: %s", err)
		return
	}
	for _, ask := range expired {
		d.withdrawAsk(ask.ID)
		if approval, found, err := d.Registry.Approval(ask.ID); err == nil && found {
			d.log.Printf("approval %s: expired unanswered: %s", ask.ID, approval.Description)
			d.tellApprovalAsker(approval, "[agenthail] The person did not answer within a day, so this was not done: "+approval.Description+". Ask again if you still need it.", registry.ApprovalExpired)
		}
		d.publishAsk(ask.SessionID)
	}
}

// An allowed action whose run the daemon may not have finished is never run
// late: the person allowed it then, not now.
func (d *Daemon) settleUnranApprovals() {
	unran, err := d.Registry.UnranApprovals()
	if err != nil {
		d.log.Printf("approvals: read unran: %s", err)
		return
	}
	for _, approval := range unran {
		if err := d.Registry.RecordApprovalRun(approval.AskID, registry.ApprovalNotRun, "the daemon stopped before it ran", time.Now()); err != nil {
			d.log.Printf("approval %s: record not run: %s", approval.AskID, err)
			continue
		}
		d.tellApprovalAsker(approval, "[agenthail] The person allowed: "+approval.Description+", but Agenthail restarted before it ran, so it was not done. Ask again if you still need it.", registry.ApprovalNotRun)
	}
}
