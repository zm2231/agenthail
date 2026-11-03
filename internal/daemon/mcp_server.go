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
	addMCPTool(server, &mcp.Tool{Name: "agenthail_channel_send", Description: "Post a message to a channel or project, delivered to its members as from you."}, d.mcpChannelSend)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_coordinator", Description: "Set or clear a project's coordinator. For the person's voice operator only."}, d.mcpCoordinator)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_archive", Description: "Archive, or restore, a session you spawned or, as coordinator, lead."}, d.mcpArchive)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_history", Description: "Delivery history, or the queue of instructions waiting to be delivered, newest first and paged. For the person's voice operator only."}, d.mcpHistory)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_ledger_write", Description: "Record in your project's intent ledger: attach, clause, question or work, with the fields the CLI's ledger commands take. Recorded as your session."}, d.mcpLedgerWrite)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_create", Description: "Start a Claude or Codex session with a first message. It is bounded by your own ceiling: no higher permission mode and no folder outside yours. Sessions nest at most 3 deep, with at most 8 running at once per spawner."}, d.mcpCreate)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_send", Description: "Send a message to a session (target) or to a project's coordinator (project). Queued by default; sessions you spawned or lead can also take mode now. A queued message may go up to your spawner or your project's coordinator, attributed to you."}, d.mcpSend)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_steer", Description: "Steer the running turn of a session you spawned or, as coordinator, lead."}, d.mcpSteer)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_interrupt", Description: "Interrupt the running turn of a session you spawned or, as coordinator, lead."}, d.mcpInterrupt)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_goal", Description: "Set, edit, clear, pause or resume the goal of a Codex session you spawned or, as coordinator, lead, or change its token budget."}, d.mcpGoal)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_answer", Description: "Allow or deny an open request (a permission prompt, question or MCP input) of a session you spawned or, as coordinator, lead. Recorded as answered by you. You never answer your own requests or those of a session above you."}, d.mcpAnswer)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_session_settings", Description: "Change the model, permission mode or effort of a session you spawned or, as coordinator, lead, or compact its context. A permission mode cannot exceed the session's ceiling."}, d.mcpSessionSettings)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_alias", Description: "Name a session you spawned or, as coordinator, lead, so it can be reached as @name."}, d.mcpAlias)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_queue", Description: "Retry or cancel a queued instruction, or clear a session's pending ones. For the person's voice operator only."}, d.mcpQueue)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_relay", Description: "Add, remove or list relays that pass one session's replies to another. For the person's voice operator only."}, d.mcpRelay)
	addMCPTool(server, &mcp.Tool{Name: "agenthail_channel", Description: "Create, delete, rename or list channels, and add or remove their members. For the person's voice operator only."}, d.mcpChannel)
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
