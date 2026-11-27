package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpRequestHashKey struct{}

func mcpRequestHash(ctx context.Context) string {
	hash, _ := ctx.Value(mcpRequestHashKey{}).(string)
	return hash
}

func (w mcpWrite) writeRequest() mcpWrite { return w }

func mcpWriteIdentity(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	if id, ok := fields["clientRequestId"].(string); ok {
		fields["clientRequestId"] = strings.TrimSpace(id)
	}
	return json.Marshal(fields)
}

type mcpReplay struct {
	done   bool
	result json.RawMessage
	err    error
}

// A retry must find its receipt before any role or scope check, since those can change between the first call and the retry.
func (d *Daemon) replayMCPWrite(ctx context.Context, caller *mcpCaller, tool string, input any) (context.Context, mcpReplay) {
	write, ok := input.(interface{ writeRequest() mcpWrite })
	if !ok {
		return ctx, mcpReplay{}
	}
	key, err := write.writeRequest().key(tool)
	if err != nil || key == "" {
		return ctx, mcpReplay{}
	}
	encoded, err := mcpWriteIdentity(input)
	if err != nil {
		return ctx, mcpReplay{done: true, err: err}
	}
	sum := sha256.Sum256(append([]byte(tool+"\n"), encoded...))
	hash := hex.EncodeToString(sum[:])
	ctx = context.WithValue(ctx, mcpRequestHashKey{}, hash)
	receipt, found, err := d.Registry.ActionReceipt(sessionPrincipal(caller.session.ID).ID, key)
	if err != nil {
		return ctx, mcpReplay{done: true, err: err}
	}
	if !found {
		return ctx, mcpReplay{}
	}
	if receipt.RequestHash != hash {
		return ctx, mcpReplay{done: true, err: refuse("idempotency_conflict", "This clientRequestId was already used for a different request; choose a new one.")}
	}
	if receipt.Status != registry.ActionReceiptCompleted {
		return ctx, mcpReplay{done: true, err: mcpUnknownOutcome()}
	}
	var stored actionReplay
	if err := json.Unmarshal(receipt.Receipt, &stored); err != nil {
		return ctx, mcpReplay{done: true, err: err}
	}
	result, refusal := mcpActionOutcome(actionReply{Status: stored.Status, Header: stored.Headers, Body: stored.Body})
	if refusal != nil {
		return ctx, mcpReplay{done: true, err: refusal}
	}
	return ctx, mcpReplay{done: true, result: result}
}

// mcpOnce keeps one outcome under key for a write that takes several actions,
// so a retry replays what the caller was told rather than any one action's.
func (d *Daemon) mcpOnce(ctx context.Context, caller *mcpCaller, key string, input any, run func() (any, error)) (any, error) {
	if key == "" {
		return run()
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var value any
	ran := false
	reply := d.idempotentReply(sessionPrincipal(caller.session.ID).ID, key, body, mcpRequestHash(ctx), func(time.Time, []byte) actionReply {
		ran = true
		value, err = run()
		return mcpOutcomeReply(value, err)
	})
	if ran {
		return value, err
	}
	result, refusal := mcpActionOutcome(reply)
	if refusal != nil {
		return nil, refusal
	}
	return result, nil
}

func mcpOutcomeReply(value any, err error) actionReply {
	status, body := http.StatusOK, any(value)
	if err != nil {
		status, body = http.StatusUnprocessableEntity, mcpStoredRefusal{Refusal: mcpRefusalFor(err)}
	}
	encoded, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		status = http.StatusUnprocessableEntity
		encoded, _ = json.Marshal(mcpStoredRefusal{Refusal: mcpRefusalFor(marshalErr)})
	}
	return actionReply{Status: status, Body: encoded}
}

type mcpStoredRefusal struct {
	Refusal *mcpRefusal `json:"mcpRefusal"`
}
