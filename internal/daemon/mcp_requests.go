package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

type mcpRequestsInput struct {
	SessionID string `json:"sessionId,omitempty" jsonschema:"one session's open requests in full; omit to list which visible sessions are waiting"`
	Limit     int    `json:"limit,omitempty" jsonschema:"entries per page, 1 to 50; default 20"`
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
	limit, err := pageLimit(input.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	if input.SessionID != "" {
		return d.mcpSessionRequests(ctx, caller, input.SessionID, input.Cursor, limit)
	}
	after, err := decodeOrderCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	visible, err := d.mcpVisibleSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	waiting := []map[string]any{}
	var lastOrder int64
	more := false
	for _, session := range visibleAfter(visible, after) {
		state, err := d.Registry.SessionWaiting(session.ID)
		if err != nil || state == nil {
			continue
		}
		if len(waiting) == limit {
			more = true
			break
		}
		row := map[string]any{"sessionId": session.ID, "kind": string(state.Kind), "requestId": state.ID, "answerable": state.Answerable}
		if alias, _ := d.Registry.ReverseAlias(session.ID); alias != "" {
			row["alias"] = alias
		}
		if !state.Since.IsZero() {
			row["since"] = state.Since.UTC().Format(time.RFC3339)
		}
		waiting = append(waiting, row)
		lastOrder = session.order
	}
	result := map[string]any{"waiting": waiting, "truncated": more}
	if more {
		result["nextCursor"] = strconv.FormatInt(lastOrder, 10)
	}
	return result, nil
}

func (d *Daemon) mcpSessionRequests(ctx context.Context, caller *mcpCaller, ref, cursor string, limit int) (any, error) {
	session, err := d.mcpReadableSession(caller, ref)
	if err != nil {
		return nil, err
	}
	requests, mode, err := d.openRequests(ctx, session)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(requests, func(a, b openRequestView) int {
		return cmp.Or(cmp.Compare(a.CreatedAt.UnixMilli(), b.CreatedAt.UnixMilli()), strings.Compare(a.ID, b.ID))
	})
	if cursor != "" {
		at, id, found := strings.Cut(cursor, ":")
		millis, parseErr := strconv.ParseInt(at, 10, 64)
		if !found || parseErr != nil {
			return nil, errBadCursor
		}
		requests = slices.DeleteFunc(requests, func(request openRequestView) bool {
			return cmp.Or(cmp.Compare(request.CreatedAt.UnixMilli(), millis), strings.Compare(request.ID, id)) <= 0
		})
	}
	views := []mcpRequestView{}
	used := 0
	for _, request := range requests {
		if len(views) == limit {
			break
		}
		view := mcpRequestView{ID: request.ID, Kind: string(request.Kind), Tool: request.Tool, Prompt: clipUTF8(request.Prompt, mcpItemTextLimit), AlwaysAllow: request.AlwaysAllow}
		if len(request.Actions) > 0 {
			view.Actions = request.Actions
		}
		if !request.CreatedAt.IsZero() {
			view.CreatedAt = request.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
		encoded, _ := json.Marshal(view)
		if used+len(encoded) > mcpPageBudget && len(views) > 0 {
			break
		}
		used += len(encoded)
		views = append(views, view)
	}
	more := len(views) < len(requests)
	result := map[string]any{"sessionId": session.ID, "permissionMode": mode, "requests": views, "truncated": more}
	if more {
		last := requests[len(views)-1]
		result["nextCursor"] = strconv.FormatInt(last.CreatedAt.UnixMilli(), 10) + ":" + last.ID
	}
	return result, nil
}
