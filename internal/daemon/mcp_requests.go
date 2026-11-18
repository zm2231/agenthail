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
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool,omitempty"`
	Prompt      string `json:"prompt"`
	Detail      string `json:"detail,omitempty"`
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
		id, alias, state, read := session.ID, "", session.WaitingOn, waitingRead{session: &session.Session}
		if session.host == nil {
			alias, _ = d.Registry.ReverseAlias(session.ID)
			if state, err = d.Registry.SessionWaiting(session.ID); err != nil {
				continue
			}
		} else {
			id, alias = hosts.Qualify(session.host.HostID, session.ID), session.host.Alias
			read = waitingRead{remoteID: id}
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
	d.describeWaiting(ctx, rows, reads)
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

type waitingRead struct {
	session   *surface.Session
	remoteID  string
	requestID string
}

// describeWaiting adds to each row the tool, prompt and detail of the request
// it names, read from the session's provider. A row whose provider has not
// answered by the deadline keeps only what the registry holds.
func (d *Daemon) describeWaiting(ctx context.Context, rows []map[string]any, reads []waitingRead) {
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
}

// openRequest is one open request of a session, or none when the session
// does not list it, as when its requests are not answered through Agenthail.
func (d *Daemon) openRequest(ctx context.Context, session *surface.Session, id string, observe bool) (openRequestView, bool) {
	open, err := d.readOpenRequests(ctx, session, observe)
	if err != nil {
		return openRequestView{}, false
	}
	return findRequest(open, id)
}

func (d *Daemon) waitingRequest(ctx context.Context, read waitingRead) (openRequestView, bool) {
	if read.remoteID == "" {
		return d.openRequest(ctx, read.session, read.requestID, false)
	}
	open, err := d.hostOpenRequests(ctx, read.remoteID)
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
	return view
}
