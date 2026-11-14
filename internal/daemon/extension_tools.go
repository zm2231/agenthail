package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/nodehost"
)

const (
	extensionToolsCapability = "tools"
	extensionToolBytes       = 2 << 10
	extensionToolTotalBytes  = 8 << 10
	extensionToolInputLimit  = 64 << 10
	extensionToolLimit       = 60 * time.Second
	mcpToolNameLimit         = 64
)

type extensionToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type offeredExtensionTool struct {
	id   string
	name string
	tool *mcp.Tool
}

// An id holds no underscore, so the first one after ext_ ends it and no two
// extensions can offer the same name.
func extensionToolName(id, name string) string { return "ext_" + id + "_" + name }

// offerExtensionTools picks the tools sessions see, in id order, within the
// per-tool and total budgets. An extension that would pass either offers
// none, so what agents see never depends on which of its tools fit.
func offerExtensionTools(loaded map[string]*loadedExtension) []offeredExtensionTool {
	ids := make([]string, 0, len(loaded))
	for id := range loaded {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var offered []offeredExtensionTool
	var listed []*mcp.Tool
	for _, id := range ids {
		ext := loaded[id]
		ext.offered, ext.toolsNote = nil, ""
		if ext.host.State != "loaded" || len(ext.host.Tools) == 0 || !slices.Contains(ext.held, extensionToolsCapability) {
			continue
		}
		tools, err := extensionMCPTools(id, ext.host.Tools)
		candidate := slices.Clone(listed)
		for _, tool := range tools {
			candidate = append(candidate, tool.tool)
		}
		if err == nil {
			if encoded, marshalErr := json.Marshal(candidate); marshalErr != nil || len(encoded) > extensionToolTotalBytes {
				err = fmt.Errorf("tools not offered: together with the extensions before it they pass the %d-byte tool budget", extensionToolTotalBytes)
			}
		}
		if err != nil {
			ext.toolsNote = err.Error()
			continue
		}
		listed = candidate
		for _, tool := range tools {
			offered = append(offered, tool)
			ext.offered = append(ext.offered, tool.tool.Name)
		}
	}
	return offered
}

func extensionMCPTools(id string, specs []extensionToolSpec) ([]offeredExtensionTool, error) {
	var tools []offeredExtensionTool
	for _, spec := range specs {
		name := extensionToolName(id, spec.Name)
		if len(name) > mcpToolNameLimit {
			return nil, fmt.Errorf("tools not offered: %s is longer than %d characters", name, mcpToolNameLimit)
		}
		var schema map[string]any
		if err := json.Unmarshal(spec.InputSchema, &schema); err != nil || schema["type"] != "object" {
			return nil, fmt.Errorf("tools not offered: %s needs an inputSchema with type \"object\"", name)
		}
		tool := &mcp.Tool{Name: name, Description: spec.Description, InputSchema: spec.InputSchema}
		encoded, err := json.Marshal(tool)
		if err != nil {
			return nil, fmt.Errorf("tools not offered: %s: %w", name, err)
		}
		if len(encoded) > extensionToolBytes {
			return nil, fmt.Errorf("tools not offered: %s is %d bytes, over the %d-byte limit for one tool", name, len(encoded), extensionToolBytes)
		}
		tools = append(tools, offeredExtensionTool{id: id, name: spec.Name, tool: tool})
	}
	return tools, nil
}

func (d *Daemon) mcpServerFor(base *mcp.Server) func(*http.Request) *mcp.Server {
	return func(r *http.Request) *mcp.Server {
		caller := mcpCallerFrom(r.Context())
		if caller == nil || caller.extension != "" {
			return base
		}
		if server := d.extensionToolServer(); server != nil {
			return server
		}
		return base
	}
}

func (d *Daemon) extensionToolServer() *mcp.Server {
	r := d.extensions
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.tools) == 0 {
		return nil
	}
	if r.toolServer == nil {
		server := d.newMCPServer()
		for _, offered := range r.tools {
			server.AddTool(offered.tool, d.extensionToolHandler(offered))
		}
		r.toolServer = server
	}
	return r.toolServer
}

func (d *Daemon) extensionToolHandler(offered offeredExtensionTool) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		caller := mcpCallerFrom(ctx)
		if caller == nil {
			return mcpRefused(mcpNoCallerRefusal(ctx)), nil
		}
		if caller.extension != "" {
			return mcpRefused(refuse("not_for_extensions", offered.tool.Name+" belongs to agent sessions; extensions cannot call it.")), nil
		}
		input := json.RawMessage(bytes.TrimSpace(request.Params.Arguments))
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		var object map[string]json.RawMessage
		if len(input) > extensionToolInputLimit || json.Unmarshal(input, &object) != nil {
			return mcpRefused(refuse("invalid", fmt.Sprintf("The arguments must be a JSON object of at most %d bytes.", extensionToolInputLimit))), nil
		}
		result, err := d.callExtensionTool(ctx, caller, offered, input)
		if err != nil {
			return mcpRefused(mcpRefusalFor(err)), nil
		}
		return mcpEncoded(result), nil
	}
}

type extensionToolCaller struct {
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	Actor     string `json:"actor"`
}

func (d *Daemon) callExtensionTool(ctx context.Context, caller *mcpCaller, offered offeredExtensionTool, input json.RawMessage) (json.RawMessage, error) {
	r := d.extensions
	token := extensionToken()
	r.mu.Lock()
	loaded, ok := r.loaded[offered.id]
	ready := ok && r.up && loaded.host.State == "loaded" && slices.Contains(loaded.offered, offered.tool.Name)
	if ready {
		r.callers[token] = scopedToExtension(caller, offered.id, loaded.held)
	}
	r.mu.Unlock()
	if !ready {
		return nil, refuse("not_found", "Extension "+offered.id+" no longer offers "+offered.tool.Name+"; it may have reloaded or lost its approval.")
	}
	defer func() {
		r.mu.Lock()
		delete(r.callers, token)
		r.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, extensionToolLimit)
	defer cancel()
	from := extensionToolCaller{Kind: string(principalSession), SessionID: d.publicSessionID(caller.session.ID), Actor: "session " + d.resolveDisplay(caller.session.ID)}
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	err := r.host.Client().Call(ctx, "tool", map[string]any{"id": offered.id, "name": offered.name, "input": input, "caller": from, "token": token}, &reply)
	if hostErr, ok := errors.AsType[*nodehost.Error](err); ok {
		return nil, refuse(hostErr.Code, hostErr.Message)
	}
	if err != nil {
		return nil, &mcpRefusal{Code: "unavailable", Message: "The extension host did not answer: " + err.Error(), Retryable: true}
	}
	if len(reply.Result) == 0 {
		reply.Result = json.RawMessage("null")
	}
	return reply.Result, nil
}

// scopedToExtension is what a tool's ctx.ah may do: what the calling session
// may, narrowed to what the extension was approved for.
func scopedToExtension(caller *mcpCaller, id string, held []string) *mcpCaller {
	scoped := *caller
	scoped.extension = id
	scoped.capabilities = nil
	for _, capability := range caller.capabilities {
		switch {
		case strings.HasPrefix(capability, "read.") && slices.Contains(held, "read.all"),
			strings.HasPrefix(capability, "control.") && slices.Contains(held, "control.all"),
			capability == "requests.answer" && slices.Contains(held, "requests.answer"):
			scoped.capabilities = append(scoped.capabilities, capability)
		}
	}
	return &scoped
}
