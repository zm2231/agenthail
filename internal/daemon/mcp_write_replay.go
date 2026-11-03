package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpRequestHashKey struct{}

func mcpRequestHash(ctx context.Context) string {
	hash, _ := ctx.Value(mcpRequestHashKey{}).(string)
	return hash
}

func (w mcpWrite) writeRequest() mcpWrite { return w }

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
	if err != nil {
		return ctx, mcpReplay{}
	}
	encoded, err := json.Marshal(input)
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
