package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpLedgerWriteInput struct {
	mcpWrite
	Op      string         `json:"op" jsonschema:"attach, clause, question or work: the same operations as agenthail ledger attach, clause, question and work."`
	Project string         `json:"project,omitempty" jsonschema:"a project by name or id, as agenthail_projects lists them; yours by default, required when you are in none"`
	Request map[string]any `json:"request" jsonschema:"The operation's fields, as the CLI sends them; your project and session are filled in for you. Unknown keys are refused. A work add nests type, priority, description, acceptance, labels, from and parent under fields (fields.type); an edit nests text, type, priority, description, acceptance, addLabels, removeLabels and parent (empty to clear) under change; undep drops a dependsOn; a park nests kind (usage, idea, later, person) and its fields under park, as the CLI sends them; a takeover nests then (release, submit or done) and force under takeover. attach also takes fragments, a list of attaches recorded in one call, each on its own."`
}

func (d *Daemon) mcpLedgerWrite(ctx context.Context, caller *mcpCaller, input mcpLedgerWriteInput) (any, error) {
	if !caller.has("ledger.write") {
		return nil, refuse("capability_missing", "Writing the ledger needs ledger.write.")
	}
	projectKey, err := d.callerLedgerKey(caller, input.Project)
	if err != nil {
		return nil, err
	}
	handlers := map[string]http.HandlerFunc{"attach": d.apiLedgerAttachHandler, "clause": d.apiLedgerClauseHandler, "question": d.apiLedgerQuestionHandler, "work": d.apiLedgerWorkHandler}
	op := strings.TrimSpace(input.Op)
	handler, ok := handlers[op]
	if !ok {
		return nil, refuse("invalid", "op is attach, clause, question or work.")
	}
	request := input.Request
	if request == nil {
		request = map[string]any{}
	}
	if owner, _ := request["owner"].(string); strings.TrimSpace(owner) != "" {
		return nil, refuse("invalid", "Ledger ownership is set by the person, not over MCP.")
	}
	request["projectKey"], request["sessionId"] = projectKey, caller.session.ID
	action, _ := request["action"].(string)
	itemID, _ := request["itemId"].(string)
	caller.remoteChange = strings.Join(strings.Fields(strings.Join([]string{op, action, itemID, "in " + projectKey}, " ")), " ")
	notice := ""
	if utterance, _ := request["utteranceId"].(string); op == "work" && ledger.WorkAction(action) == ledger.WorkTakeover && strings.TrimSpace(utterance) == "" {
		if notice, err = d.takeoverScope(caller, itemID); err != nil {
			return nil, err
		}
	}
	key, err := input.key("agenthail_ledger_write")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	call, err := http.NewRequestWithContext(sessionPrincipal(caller.session.ID).context(ctx), http.MethodPost, "/api/v1/ledger/"+op, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	call.Header.Set(actionIdempotencyHeader, key)
	recorder := newActionRecorder()
	d.ledgerWriteRoute(handler)(recorder, call)
	result, refusal := mcpActionOutcome(recorder.reply())
	d.recordMCPWrite(caller, "agenthail_ledger_write", "", input.ClientRequestID, refusal)
	if refusal != nil {
		return nil, refusal
	}
	d.noticeAfter(ctx, caller, notice, nil)
	if op == "work" && ledger.WorkAction(action) == ledger.WorkClaim {
		result = withToolGuidance(result)
	}
	if op == "work" && ledger.WorkAction(action).AttachCheckpoint() {
		return d.withLedgerCheckpoint(caller.session, result), nil
	}
	return result, nil
}

func (d *Daemon) withLedgerCheckpoint(session surface.Session, result json.RawMessage) json.RawMessage {
	text, err := d.ledgerCheckpoint(session)
	if err != nil {
		d.log.Printf("ledger checkpoint for %s: %s", session.ID, err)
		return result
	}
	var fields map[string]any
	if text == "" || json.Unmarshal(result, &fields) != nil {
		return result
	}
	fields["ledgerCheckpoint"] = text
	merged, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return merged
}

func withToolGuidance(result json.RawMessage) json.RawMessage {
	var claimed ledger.WorkResult
	if json.Unmarshal(result, &claimed) != nil || claimed.Guidance == "" {
		return result
	}
	var fields map[string]any
	if json.Unmarshal(result, &fields) != nil {
		return result
	}
	fields["guidance"] = ledger.HolderGuidance(claimed.Item.ID, true)
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return out
}
