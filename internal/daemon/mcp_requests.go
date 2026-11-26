package daemon

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpPromptFloor = 256
	mcpDetailLimit = 2 << 10
)

type mcpRequestsInput struct {
	SessionID string `json:"sessionId,omitempty" jsonschema:"one session's open requests; omit to list every visible session that is waiting"`
	RequestID string `json:"requestId,omitempty" jsonschema:"with sessionId, one open request with its prompt up to 4 KiB"`
}

type mcpRequestView struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Tool        string      `json:"tool,omitempty"`
	Prompt      string      `json:"prompt"`
	Detail      string      `json:"detail,omitempty"`
	Clipped     bool        `json:"clipped,omitempty"`
	Actions     any         `json:"actions,omitempty"`
	AlwaysAllow bool        `json:"alwaysAllow,omitempty"`
	CreatedAt   string      `json:"createdAt,omitempty"`
	Ask         *requestAsk `json:"ask,omitempty"`
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
	remote, err := d.mcpVisibleHostSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	visible = append(visible, remote...)
	sort.Slice(visible, func(i, j int) bool { return visible[i].order < visible[j].order })
	rows := []map[string]any{}
	var reads []waitingRead
	for i := range visible {
		session := &visible[i]
		id, alias, state, read := session.ID, "", session.WaitingOn, waitingRead{session: &session.Session, reader: caller.session.ID}
		if session.host == nil {
			alias, _ = d.Registry.ReverseAlias(session.ID)
			if state, err = d.Registry.SessionWaiting(session.ID); err != nil {
				continue
			}
		} else {
			id, alias = hosts.Qualify(session.host.HostID, session.ID), session.host.Alias
			read = waitingRead{remoteID: id, reader: caller.session.ID}
			if ask := session.host.AgentAsk; ask != "" {
				row := map[string]any{"sessionId": id, "kind": string(surface.WaitingAsk), "requestId": ask, "answerable": true}
				if alias != "" {
					row["alias"] = alias
				}
				confirm := read
				confirm.requestID, confirm.confirm = ask, true
				rows = append(rows, row)
				reads = append(reads, confirm)
			}
		}
		if state == nil {
			continue
		}
		row := map[string]any{"sessionId": id, "kind": string(state.Kind), "requestId": state.ID, "answerable": state.Answerable}
		if alias != "" {
			row["alias"] = alias
		}
		if !state.Since.IsZero() {
			row["since"] = state.Since.UTC().Format(time.RFC3339)
		}
		read.requestID = state.ID
		rows = append(rows, row)
		reads = append(reads, read)
	}
	local, err := d.asksRoutedTo(caller.session.ID)
	if err != nil {
		return nil, err
	}
	found := d.describeWaiting(ctx, rows, reads)
	listed := local
	for i, row := range rows {
		if found[i] != nil || !reads[i].confirm {
			listed = append(listed, row)
		}
	}
	rows = listed
	waiting := []map[string]any{}
	used := 0
	for _, row := range rows {
		encoded, _ := apitime.Marshal(row)
		if used+len(encoded) > mcpPageBudget {
			continue
		}
		used += len(encoded)
		waiting = append(waiting, row)
	}
	total := len(rows)
	return map[string]any{"waiting": waiting, "total": total, "truncated": len(waiting) < total}, nil
}

func (d *Daemon) mcpSessionRequests(ctx context.Context, caller *mcpCaller, ref, requestID string) (any, error) {
	sessionID, open, err := d.mcpSessionOpenRequests(ctx, caller, ref)
	if err != nil {
		return nil, err
	}
	requests, mode := open.requests, open.permissionMode
	slices.SortFunc(requests, func(a, b openRequestView) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	if requestID != "" {
		for _, request := range requests {
			if request.ID == requestID {
				reply := map[string]any{"sessionId": sessionID, "permissionMode": mode, "request": requestView(request, mcpItemTextLimit)}
				if open.providerError != "" {
					reply["providerError"] = open.providerError
				}
				return reply, nil
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
	reply := map[string]any{"sessionId": sessionID, "permissionMode": mode, "requests": views, "total": len(requests), "truncated": len(views) < len(requests)}
	if open.providerError != "" {
		reply["providerError"] = open.providerError
	}
	return reply, nil
}

// waitingListReads bounds how many providers one list of waiting sessions
// reads at once; all of them share one surfaceOperationTimeout.
const waitingListReads = 6

// A read to confirm is an ask a paired host's row shows routed to an agent:
// its row is listed only when that host lists the ask for the reader.
type waitingRead struct {
	session   *surface.Session
	remoteID  string
	requestID string
	reader    string
	confirm   bool
}

// describeWaiting adds to each row the tool, prompt and detail of the request
// it names, read from the session's provider, and returns what it found. A
// row whose provider has not answered by the deadline keeps only what the
// registry holds.
func (d *Daemon) describeWaiting(ctx context.Context, rows []map[string]any, reads []waitingRead) []*openRequestView {
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	found := make([]*openRequestView, len(reads))
	slots := make(chan struct{}, waitingListReads)
	var wg sync.WaitGroup
	for i, read := range reads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			if request, ok := d.waitingRequest(ctx, read); ok {
				found[i] = &request
			}
		}()
	}
	wg.Wait()
	for i, request := range found {
		if request == nil {
			continue
		}
		row := rows[i]
		describeAsk(row, request.Ask, request.CreatedAt)
		row["tool"], row["prompt"] = request.Tool, clipUTF8(request.Prompt, mcpPromptFloor)
		clipped := len(row["prompt"].(string)) < len(request.Prompt)
		if detail := request.Detail(); detail != "" {
			row["detail"] = clipUTF8(detail, mcpPromptFloor)
			clipped = clipped || len(row["detail"].(string)) < len(detail)
		}
		if clipped {
			row["clipped"] = true
		}
	}
	return found
}

// asksRoutedTo lists the open asks of sessions here routed to reader,
// whether or not it may read their sessions: the routing makes them its to
// answer.
func (d *Daemon) asksRoutedTo(reader string) ([]map[string]any, error) {
	asks, err := d.Registry.OpenAsks()
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, ask := range asks {
		if ask.Origin == registry.AskOriginPermission || !d.routedTo(ask, reader) {
			continue
		}
		row := map[string]any{"sessionId": ask.SessionID, "kind": string(surface.WaitingAsk), "requestId": ask.ID, "answerable": true, "prompt": clipUTF8(ask.Title, mcpPromptFloor)}
		if alias, _ := d.Registry.ReverseAlias(ask.SessionID); alias != "" {
			row["alias"] = alias
		}
		describeAsk(row, newRequestAsk(ask), ask.CreatedAt)
		rows = append(rows, row)
	}
	return rows, nil
}

func describeAsk(row map[string]any, ask *requestAsk, since time.Time) {
	if ask == nil {
		return
	}
	row["ask"] = ask
	if _, set := row["since"]; !set && !since.IsZero() {
		row["since"] = since.UTC().Format(time.RFC3339)
	}
}

func (d *Daemon) waitingRequest(ctx context.Context, read waitingRead) (openRequestView, bool) {
	if read.remoteID == "" {
		open, err := d.readOpenRequests(ctx, read.session, false, read.reader)
		if err != nil {
			return openRequestView{}, false
		}
		return findRequest(open, read.requestID)
	}
	open, err := d.hostOpenRequests(ctx, read.remoteID, read.reader)
	if err != nil {
		return openRequestView{}, false
	}
	return findRequest(open, read.requestID)
}

func findRequest(open openRequests, id string) (openRequestView, bool) {
	for _, request := range open.requests {
		if request.ID == id {
			return request, true
		}
	}
	return openRequestView{}, false
}

func requestView(request openRequestView, promptLimit int) mcpRequestView {
	detail := request.Detail()
	view := mcpRequestView{ID: request.ID, Kind: string(request.Kind), Tool: request.Tool, Prompt: clipUTF8(request.Prompt, promptLimit), Detail: clipUTF8(detail, min(promptLimit, mcpDetailLimit)), AlwaysAllow: request.AlwaysAllow}
	view.Clipped = len(view.Prompt) < len(request.Prompt) || len(view.Detail) < len(detail)
	if len(request.Actions) > 0 {
		view.Actions = request.Actions
	}
	if !request.CreatedAt.IsZero() {
		view.CreatedAt = request.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	view.Ask = request.Ask
	return view
}
