package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpWrite struct {
	ClientRequestID string `json:"clientRequestId,omitempty" jsonschema:"Required. A key you choose for this request; repeating it replays the first result instead of acting twice."`
}

func (w mcpWrite) key(tool string) (string, error) {
	id := strings.TrimSpace(w.ClientRequestID)
	if id == "" {
		return "", refuse("invalid", "clientRequestId is required on every write; choose one per request and reuse it only to retry.")
	}
	key := tool + ":" + id
	if len(key) > registry.ActionReceiptKeyLimit {
		return "", refuse("invalid", fmt.Sprintf("clientRequestId is too long; keep it under %d characters.", registry.ActionReceiptKeyLimit-len(tool)-1))
	}
	return key, nil
}

func (d *Daemon) mcpAction(ctx context.Context, caller *mcpCaller, tool string, write mcpWrite, request dashboardActionRequest) (json.RawMessage, error) {
	key, err := write.key(tool)
	if err != nil {
		return nil, err
	}
	request.Author = ""
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	reply := d.performAction(ctx, sessionPrincipal(caller.session.ID), actionFormatAPIv1, body, key)
	result, refusal := mcpActionOutcome(reply)
	d.recordMCPWrite(caller, tool, request.SessionID, write.ClientRequestID, refusal)
	if refusal != nil {
		return nil, refusal
	}
	return result, nil
}

func mcpActionOutcome(reply actionReply) (json.RawMessage, *mcpRefusal) {
	if reply.Status == http.StatusAccepted {
		var submitted struct{ Status string }
		if json.Unmarshal(reply.Body, &submitted) == nil && submitted.Status == "submitted" {
			return nil, &mcpRefusal{Code: "busy", Message: "A request with this clientRequestId is still running or its outcome was not recorded; retry with the same clientRequestId to read its result.", Retryable: true, Detail: map[string]any{"outcome": "unknown"}}
		}
	}
	if reply.Status < http.StatusMultipleChoices {
		return json.RawMessage(reply.Body), nil
	}
	var failure struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(reply.Body, &failure)
	if failure.Error.Code == "action_idempotency_mismatch" {
		return nil, refuse("idempotency_conflict", "This clientRequestId was already used for a different request; choose a new one.")
	}
	var extra map[string]any
	_ = json.Unmarshal(reply.Body, &extra)
	delete(extra, "error")
	return nil, mcpRefusalFor(&actionError{Status: reply.Status, Code: failure.Error.Code, Message: failure.Error.Message, Extra: extra})
}

func (d *Daemon) recordMCPWrite(caller *mcpCaller, tool, target, clientRequestID string, refusal *mcpRefusal) {
	outcome := "ok"
	if refusal != nil {
		outcome = refusal.Code
	}
	if err := d.Registry.RecordHistory(registry.HistoryEntry{Kind: "mcp", SessionID: target, SourceSessionID: caller.session.ID, Message: tool + " " + clientRequestID, Result: outcome}); err != nil {
		d.log.Printf("mcp: audit %s: %s", tool, err)
	}
}
