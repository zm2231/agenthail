package acpsurface

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/acp"
	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *ACP) requests(state acphost.State) []surface.Request {
	requests := make([]surface.Request, 0, len(state.Requests))
	for _, request := range state.Requests {
		requests = append(requests, approval(a.agent.DisplayName(), state, request))
	}
	return requests
}

func approval(agent string, state acphost.State, request acphost.Request) surface.Request {
	tool, input := toolInput(state, request)
	title := request.Title
	if title == "" {
		title = agent + " wants to use " + tool
	}
	return surface.Request{
		ID: request.ID, Kind: surface.WaitingApproval, Tool: tool, Title: title, Input: input,
		Stoppable: true, NoReason: true,
		NoAllow:   !slices.ContainsFunc(request.Options, func(o acp.PermissionOption) bool { return o.Kind == acp.OptionAllowOnce }),
		CreatedAt: request.CreatedAt,
	}
}

func toolInput(state acphost.State, request acphost.Request) (string, json.RawMessage) {
	var raw map[string]any
	_ = json.Unmarshal(request.RawInput, &raw)
	if command, ok := raw["command"].(string); ok && (request.Kind == "execute" || request.Kind == "") && strings.TrimSpace(command) != "" {
		cwd, _ := raw["cwd"].(string)
		if cwd == "" {
			cwd = state.Cwd
		}
		input, _ := json.Marshal(map[string]string{"command": command, "cwd": cwd})
		return "Bash", input
	}
	if len(request.Diffs) == 1 {
		diff := request.Diffs[0]
		if diff.OldText == nil || *diff.OldText == "" {
			input, _ := json.Marshal(map[string]string{"file_path": diff.Path, "content": diff.NewText})
			return "Write", input
		}
		input, _ := json.Marshal(map[string]string{"file_path": diff.Path, "old_string": *diff.OldText, "new_string": diff.NewText})
		return "Edit", input
	}
	tool := request.Kind
	if tool == "" {
		tool = "tool"
	}
	return tool, request.RawInput
}
