package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpOutputLimit = 16 << 10

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
	server := mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, nil)
	d.registerMCPTools(server)
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return d.mcpAuth(http.NewCrossOriginProtection().Handler(stream))
}

func (d *Daemon) registerMCPTools(server *mcp.Server) {
	addMCPTool(server, &mcp.Tool{Name: "agenthail_whoami", Description: "Your session, alias, project, host, coordinator role, capabilities, ceiling and credential expiry."}, d.mcpWhoami)
}

func addMCPTool[In any](server *mcp.Server, tool *mcp.Tool, run func(context.Context, *mcpCaller, In) (any, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, input In) (*mcp.CallToolResult, any, error) {
		caller := mcpCallerFrom(ctx)
		if caller == nil {
			return mcpRefused(refuse("unauthenticated", "A valid Agenthail session credential is required.")), nil, nil
		}
		value, err := run(ctx, caller, input)
		if err != nil {
			var refusal *mcpRefusal
			if !errors.As(err, &refusal) {
				refusal = &mcpRefusal{Code: "refused", Message: err.Error(), Retryable: true}
			}
			return mcpRefused(refusal), nil, nil
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
