package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	segmentjson "github.com/segmentio/encoding/json"
	"github.com/zm2231/agenthail/internal/registry"
)

type eventCallOutcome struct {
	event, url, result string
	reason             string
	err                error
}

// eventParams keeps a call's params undecoded, so a call whose params do not decode is still recorded.
type eventParams struct {
	mcp.ParamsBase
	raw json.RawMessage
}

func (p *eventParams) UnmarshalJSON(data []byte) error {
	p.raw = slices.Clone(data)
	return nil
}

func decodedEventCall[P any, R mcp.Result](method string, m *remoteMCP, handler func(context.Context, *mcp.ServerSession, *P) (R, error)) func(context.Context, *mcp.ServerSession, *eventParams) (R, error) {
	return func(ctx context.Context, session *mcp.ServerSession, params *eventParams) (R, error) {
		decoded := new(P)
		if len(params.raw) > 0 && string(params.raw) != "null" {
			decoder := segmentjson.NewDecoder(bytes.NewReader(params.raw))
			decoder.DontMatchCaseInsensitiveStructFields()
			if err := decoder.Decode(decoded); err != nil {
				var zero R
				refusal := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("handling '%s': params do not decode: %v", method, err)}
				if principal, err := m.eventCall(ctx, method); err == nil {
					m.recordEventCall(principal, method, eventCallOutcome{err: refusal, reason: "invalid params: they do not decode"})
				}
				return zero, refusal
			}
		}
		return handler(ctx, session, decoded)
	}
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
	switch outcome.event {
	case "":
	case eventInboxUnread:
		message += " " + outcome.event
	default:
		message += " an unknown event"
	}
	if outcome.url != "" {
		message += " to " + clip(eventURLForHistory(outcome.url), 256)
	}
	result := outcome.result
	if outcome.err != nil {
		reason := outcome.reason
		if reason == "" {
			reason = eventErrorReason(outcome.err)
		}
		result = "refused: " + strings.Join(slices.DeleteFunc([]string{reason, outcome.result}, func(part string) bool { return part == "" }), "; ")
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
