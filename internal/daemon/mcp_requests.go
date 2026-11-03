package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

type mcpRequestsInput struct {
	SessionID string `json:"sessionId,omitempty" jsonschema:"one session's open requests in full; omit to list which visible sessions are waiting"`
	Limit     int    `json:"limit,omitempty" jsonschema:"waiting sessions per page, 1 to 50; default 20"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

type mcpRequestView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool,omitempty"`
	Prompt      string `json:"prompt"`
	Actions     any    `json:"actions,omitempty"`
	AlwaysAllow bool   `json:"alwaysAllow,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

func (d *Daemon) mcpRequests(ctx context.Context, caller *mcpCaller, input mcpRequestsInput) (any, error) {
	if input.SessionID != "" {
		session, err := d.mcpReadableSession(caller, input.SessionID)
		if err != nil {
			return nil, err
		}
		requests, mode, err := d.openRequests(ctx, session)
		if err != nil {
			return nil, err
		}
		views := []mcpRequestView{}
		used := 0
		for _, request := range requests {
			view := mcpRequestView{ID: request.ID, Kind: string(request.Kind), Tool: request.Tool, Prompt: clipUTF8(request.Prompt, mcpItemTextLimit), AlwaysAllow: request.AlwaysAllow}
			if len(request.Actions) > 0 {
				view.Actions = request.Actions
			}
			if !request.CreatedAt.IsZero() {
				view.CreatedAt = request.CreatedAt.UTC().Format(time.RFC3339)
			}
			encoded, _ := json.Marshal(view)
			if used+len(encoded) > mcpPageBudget && len(views) > 0 {
				break
			}
			used += len(encoded)
			views = append(views, view)
		}
		return map[string]any{"sessionId": session.ID, "permissionMode": mode, "requests": views, "truncated": len(views) < len(requests)}, nil
	}
	limit, err := pageLimit(input.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	offset, err := decodeOffsetCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	visible, err := d.mcpVisibleSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	waiting := []map[string]any{}
	for _, session := range visible {
		state, err := d.Registry.SessionWaiting(session.ID)
		if err != nil || state == nil {
			continue
		}
		alias, _ := d.Registry.ReverseAlias(session.ID)
		row := map[string]any{"sessionId": session.ID, "kind": string(state.Kind), "requestId": state.ID, "answerable": state.Answerable}
		if alias != "" {
			row["alias"] = alias
		}
		if !state.Since.IsZero() {
			row["since"] = state.Since.UTC().Format(time.RFC3339)
		}
		waiting = append(waiting, row)
	}
	end := min(offset+limit, len(waiting))
	result := map[string]any{"waiting": waiting[min(offset, end):end], "total": len(waiting), "truncated": end < len(waiting)}
	if end < len(waiting) {
		result["nextCursor"] = strconv.Itoa(end)
	}
	return result, nil
}
