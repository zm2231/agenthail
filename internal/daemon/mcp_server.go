package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpOutputLimit = 16 << 10

// Every tool answers in structuredContent, which 2025-06-18 introduced.
var mcpProtocolVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

type mcpRefusal struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Detail    map[string]any `json:"detail,omitempty"`
}

func (r *mcpRefusal) Error() string { return r.Code + ": " + r.Message }

func refuse(code, message string) *mcpRefusal {
	return &mcpRefusal{Code: code, Message: message}
}

func (d *Daemon) mcpHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{SupportedProtocolVersions: mcpProtocolVersions})
	d.registerMCPTools(server)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return d.mcpAuth(http.NewCrossOriginProtection().Handler(stream))
}

func (d *Daemon) registerMCPTools(server *mcp.Server) {
	addMCPTool(server, &mcp.Tool{Name: "agenthail_whoami", Description: "Your session, alias, project, host, coordinator role, capabilities, ceiling and credential expiry."}, d.mcpWhoami)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_sessions", Description: "Sessions you may read: your own, the ones you spawned, and your project's. Paged; pass nextCursor back as cursor. A session that appears while you page shows up when you list again from the start."}, d.mcpSessions)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_capabilities", Description: "What you may do: capabilities, ceiling, which sessions you can read, which surfaces you may start, and the output limits."}, d.mcpCapabilities)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_requests", Description: "The sessions you can read that are waiting on a prompt or question, or one session's open requests, in one call (prompts clipped to fit; pass requestId for one request's longer prompt). If truncated is true, total says how many there are."}, d.mcpRequests)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_ledger_read", Description: "Your project's intent ledger: the board, or one list (goals, asks, decisions, questions, work, ready), paged."}, d.mcpLedgerRead)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_read", Description: "A session's latest items, older pages through before, newer items through since, or a clipped item's full text through bodyRef. Results are capped at 16 KiB."}, d.mcpRead)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_projects", Description: "The projects on this host and its paired hosts, with their coordinators. A project marked conflict has coordinators on two hosts and cannot be reached until one is chosen."}, d.mcpProjects)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_send", Description: "Send a message to a session (target) or to a project's coordinator (project). Queued by default; sessions you spawned or lead can also take mode now. A queued message may go up to your spawner or your project's coordinator, attributed to you."}, d.mcpSend)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_steer", Description: "Steer the running turn of a session you spawned or, as coordinator, lead."}, d.mcpSteer)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_interrupt", Description: "Interrupt the running turn of a session you spawned or, as coordinator, lead."}, d.mcpInterrupt)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_goal", Description: "Set, edit, clear, pause or resume the goal of a Codex session you spawned or, as coordinator, lead, or change its token budget."}, d.mcpGoal)
}

func addMCPTool[In any](server *mcp.Server, tool *mcp.Tool, run func(context.Context, *mcpCaller, In) (any, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		caller := mcpCallerFrom(ctx)
		if caller == nil {
			return mcpRefused(refuse("unauthenticated", "A valid Agenthail session credential is required.")), nil, nil
		}
		value, err := run(ctx, caller, input)
		if err != nil {
			return mcpRefused(mcpRefusalFor(err)), nil, nil
		}
		encoded, err := json.Marshal(value)
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
