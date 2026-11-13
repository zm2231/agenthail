package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/apitime"
)

const mcpOutputLimit = 16 << 10

// Every tool answers in structuredContent, which 2025-06-18 introduced.
var mcpProtocolVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

type mcpRefusal struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Detail    map[string]any `json:"detail,omitempty"`
	target    string
	audited   bool
}

func (r *mcpRefusal) Error() string { return r.Code + ": " + r.Message }

func (r *mcpRefusal) about(target string) *mcpRefusal {
	r.target = target
	return r
}

func refuse(code, message string) *mcpRefusal {
	return &mcpRefusal{Code: code, Message: message}
}

func scopeRefusal(target, rule, message string) *mcpRefusal {
	return &mcpRefusal{Code: "out_of_scope", Message: message, Detail: map[string]any{"rule": rule}, target: target}
}

func (d *Daemon) mcpHandler() http.Handler {
	server := d.newMCPServer()
	d.registerMCPTools(server)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, mcpStreamOptions)
	return d.mcpAuth(http.NewCrossOriginProtection().Handler(stream))
}

var mcpStreamOptions = &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}

func (d *Daemon) newMCPServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{SupportedProtocolVersions: mcpProtocolVersions})
}

func (d *Daemon) registerMCPTools(server *mcp.Server) {
	handlers := d.mcpHandlers()
	for _, tool := range agenttools.Tools {
		register, ok := handlers[tool.Name]
		if !ok {
			panic("agenthail MCP tool " + tool.Name + " has no handler")
		}
		register(server, mcpTool(tool))
	}
}

const mcpAlwaysLoadMeta = "anthropic/alwaysLoad"

func mcpTool(tool agenttools.Tool) *mcp.Tool {
	out := &mcp.Tool{Name: tool.Name, Description: tool.Description}
	if tool.AlwaysLoad {
		out.Meta = mcp.Meta{mcpAlwaysLoadMeta: true}
	}
	return out
}

type mcpRegistrar func(*mcp.Server, *mcp.Tool)

func bindMCP[In any](d *Daemon, run func(context.Context, *mcpCaller, In) (any, error)) mcpRegistrar {
	return func(server *mcp.Server, tool *mcp.Tool) { addMCPTool(d, server, tool, run) }
}

func (d *Daemon) mcpHandlers() map[string]mcpRegistrar {
	return map[string]mcpRegistrar{
		"agenthail_whoami":           bindMCP(d, d.mcpWhoami),
		"agenthail_sessions":         bindMCP(d, d.mcpSessions),
		"agenthail_capabilities":     bindMCP(d, d.mcpCapabilities),
		"agenthail_requests":         bindMCP(d, d.mcpRequests),
		"agenthail_ledger_read":      bindMCP(d, d.mcpLedgerRead),
		"agenthail_read":             bindMCP(d, d.mcpRead),
		"agenthail_projects":         bindMCP(d, d.mcpProjects),
		"agenthail_channel_send":     bindMCP(d, d.mcpChannelSend),
		"agenthail_coordinator":      bindMCP(d, d.mcpCoordinator),
		"agenthail_archive":          bindMCP(d, d.mcpArchive),
		"agenthail_history":          bindMCP(d, d.mcpHistory),
		"agenthail_ledger_write":     bindMCP(d, d.mcpLedgerWrite),
		"agenthail_create":           bindMCP(d, d.mcpCreate),
		agenttools.EndTurn:           bindMCP(d, d.mcpEndTurn),
		agenttools.Ask:               bindMCP(d, d.mcpAsk),
		"agenthail_report":           bindMCP(d, d.mcpReport),
		"agenthail_result":           bindMCP(d, d.mcpResult),
		"agenthail_wait":             bindMCP(d, d.mcpWait),
		"agenthail_send":             bindMCP(d, d.mcpSend),
		"agenthail_steer":            bindMCP(d, d.mcpSteer),
		"agenthail_interrupt":        bindMCP(d, d.mcpInterrupt),
		"agenthail_goal":             bindMCP(d, d.mcpGoal),
		"agenthail_answer":           bindMCP(d, d.mcpAnswer),
		"agenthail_session_settings": bindMCP(d, d.mcpSessionSettings),
		"agenthail_alias":            bindMCP(d, d.mcpAlias),
		"agenthail_pr":               bindMCP(d, d.mcpPR),
		"agenthail_queue":            bindMCP(d, d.mcpQueue),
		"agenthail_relay":            bindMCP(d, d.mcpRelay),
		"agenthail_channel":          bindMCP(d, d.mcpChannel),
		"agenthail_browser":          bindMCP(d, d.mcpBrowser),
		"agenthail_simulator":        bindMCP(d, d.mcpSimulator),
	}
}

func addMCPTool[In any](d *Daemon, server *mcp.Server, tool *mcp.Tool, run func(context.Context, *mcpCaller, In) (any, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		caller := mcpCallerFrom(ctx)
		if caller == nil {
			return mcpRefused(&mcpRefusal{Code: "unauthenticated", Message: "This session is still starting; try again in a moment.", Retryable: true}), nil, nil
		}
		if _, allowed := extensionTools[tool.Name]; caller.extension != "" && !allowed {
			return mcpRefused(&mcpRefusal{Code: "not_for_extensions", Message: tool.Name + " belongs to agent sessions; extensions cannot call it."}), nil, nil
		}
		ctx, replay := d.replayMCPWrite(ctx, caller, tool.Name, input)
		var value any
		var err error
		if replay.done {
			value, err = replay.result, replay.err
		} else {
			value, err = run(ctx, caller, input)
		}
		if err != nil {
			refusal := mcpRefusalFor(err)
			if !replay.done {
				d.recordMCPScopeRefusal(caller, tool.Name, refusal)
			}
			return mcpRefused(refusal), nil, nil
		}
		encoded, err := apitime.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		if len(encoded) > mcpOutputLimit {
			return mcpRefused(&mcpRefusal{Code: "invalid", Message: fmt.Sprintf("The result exceeds %d bytes; ask for a smaller limit.", mcpOutputLimit)}), nil, nil
		}
		return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
	})
}

func mcpRefused(refusal *mcpRefusal) *mcp.CallToolResult {
	encoded, _ := json.Marshal(refusal)
	return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
}
