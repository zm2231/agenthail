package daemon

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
)

const mcpPromptFloor = 256

type mcpRequestsInput struct {
	SessionID string `json:"sessionId,omitempty" jsonschema:"one session's open requests; omit to list every visible session that is waiting"`
	RequestID string `json:"requestId,omitempty" jsonschema:"with sessionId, one open request with its prompt up to 4 KiB"`
}

type mcpRequestView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool,omitempty"`
	Prompt      string `json:"prompt"`
	Clipped     bool   `json:"clipped,omitempty"`
	Actions     any    `json:"actions,omitempty"`
	AlwaysAllow bool   `json:"alwaysAllow,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

func (d *Daemon) mcpRequests(ctx context.Context, caller *mcpCaller, input mcpRequestsInput) (any, error) {
	if input.SessionID != "" {
		return d.mcpSessionRequests(ctx, caller, input.SessionID, input.RequestID)
	}
	if input.RequestID != "" {
		return nil, refuse("invalid", "requestId needs the sessionId it belongs to.")
	}
	visible, err := d.mcpVisibleSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	waiting := []map[string]any{}
	used, total := 0, 0
	for _, session := range visible {
		state, err := d.Registry.SessionWaiting(session.ID)
		if err != nil || state == nil {
			continue
		}
		total++
		row := map[string]any{"sessionId": session.ID, "kind": string(state.Kind), "requestId": state.ID, "answerable": state.Answerable}
		if alias, _ := d.Registry.ReverseAlias(session.ID); alias != "" {
			row["alias"] = alias
		}
		if !state.Since.IsZero() {
			row["since"] = state.Since.UTC().Format(time.RFC3339)
		}
		encoded, _ := apitime.Marshal(row)
		if used+len(encoded) > mcpPageBudget {
			continue
		}
		used += len(encoded)
		waiting = append(waiting, row)
	}
	return map[string]any{"waiting": waiting, "total": total, "truncated": len(waiting) < total}, nil
}

func (d *Daemon) mcpSessionRequests(ctx context.Context, caller *mcpCaller, ref, requestID string) (any, error) {
	session, err := d.mcpReadableSession(caller, ref)
	if err != nil {
		return nil, err
	}
	requests, mode, err := d.openRequests(ctx, session)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(requests, func(a, b openRequestView) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	if requestID != "" {
		for _, request := range requests {
			if request.ID == requestID {
				return map[string]any{"sessionId": session.ID, "permissionMode": mode, "request": requestView(request, mcpItemTextLimit)}, nil
			}
		}
		return nil, refuse("not_found", fmt.Sprintf("Request %s is not open on this session.", requestID))
	}
	promptLimit := mcpItemTextLimit
	if len(requests) > 0 {
		promptLimit = max(mcpPromptFloor, min(mcpItemTextLimit, mcpPageBudget/len(requests)-mcpPromptFloor))
	}
	views := []mcpRequestView{}
	used := 0
	for _, request := range requests {
		view := requestView(request, promptLimit)
		encoded, _ := apitime.Marshal(view)
		if used+len(encoded) > mcpPageBudget {
			break
		}
		used += len(encoded)
		views = append(views, view)
	}
	return map[string]any{"sessionId": session.ID, "permissionMode": mode, "requests": views, "total": len(requests), "truncated": len(views) < len(requests)}, nil
}

func requestView(request openRequestView, promptLimit int) mcpRequestView {
	view := mcpRequestView{ID: request.ID, Kind: string(request.Kind), Tool: request.Tool, Prompt: clipUTF8(request.Prompt, promptLimit), AlwaysAllow: request.AlwaysAllow}
	view.Clipped = len(view.Prompt) < len(request.Prompt)
	if len(request.Actions) > 0 {
		view.Actions = request.Actions
	}
	if !request.CreatedAt.IsZero() {
		view.CreatedAt = request.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return view
}
