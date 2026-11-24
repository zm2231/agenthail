package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/apitime"
)

const mcpOutputLimit = 16 << 10

// Every tool answers in structuredContent, which 2025-06-18 introduced.
var mcpProtocolVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

type mcpRefusal struct {
	Code         string         `json:"code"`
	Message      string         `json:"message"`
	Retryable    bool           `json:"retryable"`
	Detail       map[string]any `json:"detail,omitempty"`
	target       string
	actionCode   string
	audited      bool
	crossProject bool
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
	stream := mcp.NewStreamableHTTPHandler(d.mcpServerFor(d.newMCPServer()), mcpStreamOptions)
	local := d.mcpAuth(http.NewCrossOriginProtection().Handler(stream))
	remote := d.remoteMCP().handler
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remoteRequest(r) {
			remote.ServeHTTP(w, r)
			return
		}
		local.ServeHTTP(w, r)
	})
}

var mcpStreamOptions = &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}

func (d *Daemon) newMCPServer() *mcp.Server {
	server := d.newBareMCPServer()
	d.registerMCPTools(server)
	return server
}

func (d *Daemon) newBareMCPServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{SupportedProtocolVersions: mcpProtocolVersions, Instructions: agenttools.ServerInstructions})
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
	server.AddReceivingMiddleware(d.roleToolList)
}

// roleToolList leaves out of a tool list the agenthail tools the caller's
// role does not see; callMCPTool refuses them too.
func (d *Daemon) roleToolList(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, req)
		list, ok := result.(*mcp.ListToolsResult)
		if err != nil || !ok {
			return result, err
		}
		caller := mcpCallerFrom(ctx)
		if _, info := remoteName(req.GetExtra()); caller == nil && info != nil {
			if caller, err = d.remoteCaller(info); err != nil {
				return nil, err
			}
		}
		if caller == nil || caller.role == nil {
			return result, nil
		}
		seen := make([]*mcp.Tool, 0, len(list.Tools))
		for _, tool := range list.Tools {
			if _, agenthail := agenttools.Lookup(tool.Name); !agenthail || caller.role.Sees(tool.Name) {
				seen = append(seen, tool)
			}
		}
		filtered := *list
		filtered.Tools = seen
		return &filtered, nil
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
		"agenthail_brief":            bindMCP(d, d.mcpBrief),
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
	mcp.AddTool(server, tool, func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		started := time.Now()
		caller := mcpCallerFrom(ctx)
		if _, info := remoteName(req.Extra); caller == nil && info != nil {
			remote, err := d.remoteCaller(info)
			if err != nil {
				return nil, nil, err
			}
			caller = remote
		}
		result, refusal, err := callMCPTool(d, ctx, caller, tool.Name, input, run)
		d.logMCPCall(ctx, caller, tool.Name, time.Since(started), refusal)
		d.recordRemoteCall(caller, tool.Name, refusal)
		return result, nil, err
	})
}

// callMCPTool answers within the tool's bound; past it the caller is told so
// and the work, which cannot be cancelled everywhere, finishes on its own.
func callMCPTool[In any](d *Daemon, ctx context.Context, caller *mcpCaller, tool string, input In, run func(context.Context, *mcpCaller, In) (any, error)) (*mcp.CallToolResult, *mcpRefusal, error) {
	if caller == nil {
		refusal := mcpNoCallerRefusal(ctx)
		return mcpRefused(refusal), refusal, nil
	}
	if _, allowed := extensionTools[tool]; caller.extension != "" && !allowed {
		refusal := &mcpRefusal{Code: "not_for_extensions", Message: tool + " belongs to agent sessions; extensions cannot call it."}
		return mcpRefused(refusal), refusal, nil
	}
	if !caller.role.Lists(tool) {
		refusal := &mcpRefusal{Code: "capability_missing", Message: tool + " is not among the tools your role, " + caller.role.Name + ", sees.", Detail: map[string]any{"rule": "role_tools", "role": caller.role.Name}}
		return mcpRefused(refusal), refusal, nil
	}
	_, waits := any(input).(mcpOwnTimeout)
	cancelAtBound := waits || mcpReadTools[tool]
	value, err := withinBound(d, ctx, tool, mcpCallBound(tool, input), func(bounded context.Context) (mcpToolOutcome, error) {
		work := ctx
		if cancelAtBound {
			work = bounded
		}
		work, replay := d.replayMCPWrite(work, caller, tool, input)
		if replay.done {
			return mcpToolOutcome{value: replay.result, err: replay.err, replayed: true}, nil
		}
		value, err := run(work, caller, input)
		return mcpToolOutcome{value: value, err: err}, nil
	})
	var stall *mcpStallError
	if errors.As(err, &stall) {
		refusal := mcpStalledCall(tool, input, stall)
		return mcpRefused(refusal), refusal, nil
	}
	if err == nil {
		err = value.err
	}
	if err != nil {
		refusal := mcpRefusalFor(err)
		if !value.replayed {
			d.recordMCPScopeRefusal(caller, tool, refusal)
		}
		return mcpRefused(refusal), refusal, nil
	}
	encoded, err := apitime.Marshal(value.value)
	if err != nil {
		return nil, nil, err
	}
	if refusal := mcpOversize(encoded); refusal != nil {
		return mcpRefused(refusal), refusal, nil
	}
	return mcpEncoded(encoded), nil, nil
}

type mcpToolOutcome struct {
	value    any
	err      error
	replayed bool
}

func mcpOversize(encoded []byte) *mcpRefusal {
	if len(encoded) > mcpOutputLimit {
		return &mcpRefusal{Code: "invalid", Message: fmt.Sprintf("The result exceeds %d bytes; ask for a smaller limit.", mcpOutputLimit)}
	}
	return nil
}

func mcpEncoded(encoded []byte) *mcp.CallToolResult {
	if refusal := mcpOversize(encoded); refusal != nil {
		return mcpRefused(refusal)
	}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
}

func mcpRefused(refusal *mcpRefusal) *mcp.CallToolResult {
	encoded, _ := json.Marshal(refusal)
	return &mcp.CallToolResult{IsError: true, StructuredContent: json.RawMessage(encoded), Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
}
