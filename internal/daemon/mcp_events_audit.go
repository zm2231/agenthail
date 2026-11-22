package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/zm2231/agenthail/internal/registry"
)

type eventCallOutcome struct {
	name, url, result string
	err               error
}

// eventCall names the caller, and leaves a daemon log line for a call no identity made.
func (m *remoteMCP) eventCall(ctx context.Context, method string) (eventPrincipal, error) {
	principal, err := eventCaller(ctx)
	if err != nil {
		m.d.log.Printf("%s refused: not a remote agent identity", method)
	}
	return principal, err
}

// recordEventCall writes one mcp-event entry per call, never the secret or the URL's query.
func (m *remoteMCP) recordEventCall(principal eventPrincipal, method string, outcome eventCallOutcome) {
	message := method + " by @" + principal.name
	if outcome.name != "" {
		message += " " + clip(outcome.name, 64)
	}
	if outcome.url != "" {
		message += " to " + clip(eventURLForHistory(outcome.url), 256)
	}
	result := outcome.result
	if outcome.err != nil {
		result = "refused: " + strings.Join(slices.DeleteFunc([]string{eventErrorReason(outcome.err), outcome.result}, func(part string) bool { return part == "" }), "; ")
	}
	if err := m.d.Registry.RecordHistory(registry.HistoryEntry{Kind: historyMCPEvent, SourceSessionID: principal.identity, Message: message, Result: result}); err != nil {
		m.d.logRuntimeError("record an mcp event call", err)
	}
}

func eventErrorReason(err error) string {
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) {
		return clip(err.Error(), 200)
	}
	switch wire.Code {
	case codeCallbackEndpoint:
		return ""
	case jsonrpc.CodeInvalidParams:
		return "invalid params: " + clip(wire.Message, 200)
	}
	return clip(wire.Message, 200)
}

func (r webhookReply) describe() string {
	switch {
	case r.status != 0 && r.reason != "":
		return fmt.Sprintf("%s (HTTP %d)", r.reason, r.status)
	case r.status != 0:
		return fmt.Sprintf("HTTP %d", r.status)
	}
	return r.reason
}
