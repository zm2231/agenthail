package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexCommandApproval    = "item/commandExecution/requestApproval"
	codexFileChangeApproval = "item/fileChange/requestApproval"
	codexPermissionApproval = "item/permissions/requestApproval"
	codexUserInputRequest   = "item/tool/requestUserInput"
	codexRequestResolved    = "serverRequest/resolved"
)

// errCodexRequestsElsewhere is why a thread's requests are not read here: the
// route Agenthail reaches it by does not carry the app-server's requests.
var errCodexRequestsElsewhere = errors.New("this Codex thread runs where Agenthail cannot read its requests; answer it in Codex")

// errCodexRequestUnanswerable is why a thread that reports a wait lists no
// request: Codex holds one of a kind Agenthail does not answer.
var errCodexRequestUnanswerable = errors.New("Codex waits on a request Agenthail does not answer; answer it in Codex")

// codexRequestWait bounds how long a resume waits for the requests Codex
// sends a new subscriber, which arrive just after the resume's answer.
var codexRequestWait = time.Second

const codexRequestSettle = 100 * time.Millisecond

type codexAnswerer interface {
	codexClient
	codexNotifier
	Respond(id json.RawMessage, result any) error
}

// A codexOpenRequest is a request the app-server holds open on a thread,
// with the id this connection must answer it by.
type codexOpenRequest struct {
	request surface.Request
	method  string
	rpcID   json.RawMessage
	params  map[string]any
}

// preciseWait replaces a thread's flag-only wait with its oldest open
// request when the reader carries requests; otherwise the flag stands. A
// listing carries only the flag, which cannot tell one request from the next,
// so it keeps the flag-only wait and the catalog confirms it by observing.
func (c *Codex) preciseWait(ctx context.Context, client codexClient, sess *surface.Session, flagged *surface.Waiting) *surface.Waiting {
	answerer, ok := client.(codexAnswerer)
	if !ok || flagged == nil {
		return flagged
	}
	open, err := c.codexOpenRequests(ctx, answerer, sess)
	if err != nil || len(open) == 0 {
		return flagged
	}
	oldest := open[0].request
	return &surface.Waiting{Kind: oldest.Kind, ID: oldest.ID, Since: oldest.CreatedAt, Answerable: true}
}

// codexOpenRequests subscribes client to the thread and returns the requests
// the app-server sends a new subscriber, oldest first, less any resolved
// meanwhile. Requests of kinds Agenthail does not answer are left out.
func (c *Codex) codexOpenRequests(ctx context.Context, client codexAnswerer, sess *surface.Session) ([]codexOpenRequest, error) {
	client.DrainNotifications()
	if _, err := c.codexThreadRequest(ctx, client, codexThreadResume, sess, map[string]any{"threadId": sess.ID, "excludeTurns": true}, 5*time.Second); err != nil {
		return nil, fmt.Errorf("thread/resume: %w", err)
	}
	defer c.subscriptions.remove(sess.ID, client)
	byID := map[string]codexOpenRequest{}
	var order []string
	deadline := time.NewTimer(codexRequestWait)
	defer deadline.Stop()
	var settle <-chan time.Time
	for {
		for _, event := range client.DrainNotifications() {
			if str(event.Params, "threadId") != sess.ID {
				continue
			}
			if event.Method == codexRequestResolved {
				delete(byID, codexRPCKey(event.Params["requestId"]))
				continue
			}
			if len(event.RequestID) == 0 {
				continue
			}
			request, ok := codexRequest(sess.ID, event.Method, event.Params)
			if !ok {
				continue
			}
			key := codexRPCKey(event.RequestID)
			if _, seen := byID[key]; !seen {
				order = append(order, key)
			}
			byID[key] = codexOpenRequest{request: request, method: event.Method, rpcID: event.RequestID, params: event.Params}
			if settle == nil {
				settle = time.After(codexRequestSettle)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return codexOrderedRequests(byID, order), nil
		case <-settle:
			return codexOrderedRequests(byID, order), nil
		case <-client.Notified():
		}
	}
}

func codexOrderedRequests(byID map[string]codexOpenRequest, order []string) []codexOpenRequest {
	open := make([]codexOpenRequest, 0, len(byID))
	for _, key := range order {
		if request, ok := byID[key]; ok {
			open = append(open, request)
		}
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].request.CreatedAt.Before(open[j].request.CreatedAt) })
	return open
}

// codexRPCKey compares a JSON-RPC id however it was written: Codex sends
// numbers in requests and may echo them as numbers or strings.
func codexRPCKey(value any) string {
	switch id := value.(type) {
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(id, &decoded) == nil {
			return codexRPCKey(decoded)
		}
		return string(id)
	case float64:
		return fmt.Sprintf("%.0f", id)
	case string:
		return id
	}
	return fmt.Sprint(value)
}

// codexRequest reads one server request as a surface request. Its id names
// the thread and the item it gates, which every connection sees the same.
func codexRequest(threadID, method string, params map[string]any) (surface.Request, bool) {
	itemID := str(params, "itemId")
	if itemID == "" {
		return surface.Request{}, false
	}
	id := threadID + ":" + itemID
	if approval := str(params, "approvalId"); approval != "" {
		id += ":" + approval
	}
	request := surface.Request{ID: id, Kind: surface.WaitingApproval, Reason: str(params, "reason"), NoReason: true, CreatedAt: time.Now().UTC()}
	if started, ok := params["startedAtMs"].(float64); ok && started > 0 {
		request.CreatedAt = time.UnixMilli(int64(started)).UTC()
	}
	switch method {
	case codexCommandApproval:
		request.Tool = "Bash"
		request.Title = str(params, "command")
		request.Input, _ = json.Marshal(map[string]any{"command": str(params, "command"), "cwd": str(params, "cwd")})
		decisions := codexDecisions(params)
		request.AlwaysAllow = decisions.always != nil
		request.AlwaysDeny = decisions.never != nil
		request.Stoppable = decisions.decline && decisions.cancel
	case codexFileChangeApproval:
		request.Tool = "Edit"
		request.Title = "Codex wants to change files"
		if root := str(params, "grantRoot"); root != "" {
			request.BlockedPath = root
		}
		request.AlwaysAllow, request.Stoppable = true, true
	case codexPermissionApproval:
		request.Tool = "Permissions"
		request.Title = "Codex asks for more access"
		request.Input, _ = json.Marshal(params["permissions"])
		request.AlwaysAllow = true
	case codexUserInputRequest:
		request.Kind = surface.WaitingQuestion
		request.Tool = "request_user_input"
		for _, raw := range asList(params["questions"]) {
			question, _ := raw.(map[string]any)
			text := str(question, "question")
			if text == "" {
				continue
			}
			entry := surface.Question{Question: text, Header: str(question, "header")}
			for _, option := range asList(question["options"]) {
				object, _ := option.(map[string]any)
				entry.Options = append(entry.Options, surface.QuestionOption{Label: str(object, "label"), Description: str(object, "description")})
			}
			request.Questions = append(request.Questions, entry)
		}
		if len(request.Questions) == 0 {
			return surface.Request{}, false
		}
	default:
		return surface.Request{}, false
	}
	return request, true
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

// codexOffered is what a command approval offers: Codex lists its
// decisions on the request, and a request without the list offers accept,
// decline and cancel.
type codexOffered struct {
	accept, decline, cancel bool
	always, never           any
}

func codexDecisions(params map[string]any) codexOffered {
	listed, ok := params["availableDecisions"].([]any)
	if !ok {
		return codexOffered{accept: true, decline: true, cancel: true}
	}
	var offered codexOffered
	var forSession any
	for _, decision := range listed {
		switch value := decision.(type) {
		case string:
			switch value {
			case "accept":
				offered.accept = true
			case "acceptForSession":
				forSession = value
			case "decline":
				offered.decline = true
			case "cancel":
				offered.cancel = true
			}
		case map[string]any:
			if _, ok := value["acceptWithExecpolicyAmendment"]; ok {
				offered.always = value
			}
			if amendment, ok := value["applyNetworkPolicyAmendment"].(map[string]any); ok {
				rule, _ := amendment["network_policy_amendment"].(map[string]any)
				switch str(rule, "action") {
				case "allow":
					if offered.always == nil {
						offered.always = value
					}
				case "deny":
					offered.never = value
				}
			}
		}
	}
	if offered.always == nil && forSession != nil {
		offered.always = forSession
	}
	return offered
}

// codexAnswer is the result Codex takes for a decision on request.
func codexAnswer(open codexOpenRequest, decision surface.Decision) (any, string, error) {
	if decision.Behavior == "deny" && decision.Message != "" {
		return nil, "", errors.New("a denial of a Codex request takes no reason; deny it without one")
	}
	if decision.Behavior != "allow" && decision.Behavior != "deny" {
		return nil, "", fmt.Errorf("unknown behavior %q", decision.Behavior)
	}
	allow := decision.Behavior == "allow"
	switch {
	case decision.Stop && (allow || !open.request.Stoppable):
		return nil, "", errors.New("this request offers no denial that stops the turn")
	case decision.Always && allow && !open.request.AlwaysAllow:
		return nil, "", errors.New("this request offers no rule to always allow it")
	case decision.Always && !allow && !open.request.AlwaysDeny:
		return nil, "", errors.New("this request offers no rule to always deny it")
	}
	switch open.method {
	case codexCommandApproval:
		offered := codexDecisions(open.params)
		switch {
		case allow && decision.Always:
			return map[string]any{"decision": offered.always}, "allowed", nil
		case allow:
			return map[string]any{"decision": "accept"}, "allowed", nil
		case decision.Always:
			return map[string]any{"decision": offered.never}, "denied", nil
		case decision.Stop || !offered.decline:
			return map[string]any{"decision": "cancel"}, "denied", nil
		}
		return map[string]any{"decision": "decline"}, "denied", nil
	case codexFileChangeApproval:
		switch {
		case allow && decision.Always:
			return map[string]any{"decision": "acceptForSession"}, "allowed", nil
		case allow:
			return map[string]any{"decision": "accept"}, "allowed", nil
		case decision.Stop:
			return map[string]any{"decision": "cancel"}, "denied", nil
		}
		return map[string]any{"decision": "decline"}, "denied", nil
	case codexPermissionApproval:
		if !allow {
			return map[string]any{"permissions": map[string]any{}}, "denied", nil
		}
		scope := "turn"
		if decision.Always {
			scope = "session"
		}
		return map[string]any{"permissions": open.params["permissions"], "scope": scope}, "allowed", nil
	case codexUserInputRequest:
		answers := map[string]any{}
		if allow {
			for _, raw := range asList(open.params["questions"]) {
				question, _ := raw.(map[string]any)
				if answer, ok := decision.Answers[str(question, "question")]; ok && strings.TrimSpace(answer) != "" {
					answers[str(question, "id")] = map[string]any{"answers": []string{answer}}
				}
			}
			if len(answers) == 0 {
				return nil, "", errors.New("an answer to this request needs at least one answered question")
			}
		}
		if allow {
			return map[string]any{"answers": answers}, "answered", nil
		}
		return map[string]any{"answers": answers}, "denied", nil
	}
	return nil, "", fmt.Errorf("unanswered request method %s", open.method)
}

// openAnswerer reaches the thread through a connection that carries the
// app-server's requests.
func (c *Codex) openAnswerer(ctx context.Context, sess *surface.Session) (codexAnswerer, error) {
	client, err := c.openReader(ctx, sess)
	if err != nil {
		return nil, err
	}
	answerer, ok := client.(codexAnswerer)
	if !ok {
		_ = client.Close()
		return nil, errCodexRequestsElsewhere
	}
	return answerer, nil
}

// Requests lists the requests a thread waits on, read from the app-server
// now. A thread whose status reports no wait is not resumed and has none.
func (c *Codex) Requests(ctx context.Context, sess *surface.Session) (surface.RequestState, error) {
	state := surface.RequestState{Requests: []surface.Request{}}
	client, err := c.openAnswerer(ctx, sess)
	if err != nil {
		return state, err
	}
	defer client.Close()
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return state, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	if codexWaitingKind(thread["status"]) == "" {
		return state, nil
	}
	open, err := c.codexOpenRequests(ctx, client, sess)
	if err != nil {
		return state, err
	}
	if len(open) == 0 {
		return state, errCodexRequestUnanswerable
	}
	for _, request := range open {
		state.Requests = append(state.Requests, request.request)
	}
	return state, nil
}

// codexResolveWait bounds how long an answer waits for Codex to confirm it.
var codexResolveWait = 5 * time.Second

// Respond answers one open request on a connection subscribed to its thread.
// A request no longer open, or one another client answered first, is stale.
func (c *Codex) Respond(ctx context.Context, sess *surface.Session, requestID string, decision surface.Decision) (string, error) {
	if requestID == "" {
		return "", errors.New("a request id is required")
	}
	client, err := c.openAnswerer(ctx, sess)
	if err != nil {
		return "", err
	}
	defer client.Close()
	open, err := c.codexOpenRequests(ctx, client, sess)
	if err != nil {
		return "", err
	}
	index := -1
	for i, request := range open {
		if request.request.ID == requestID {
			index = i
		}
	}
	if index < 0 {
		return "", surface.ErrRequestStale
	}
	target := open[index]
	result, outcome, err := codexAnswer(target, decision)
	if err != nil {
		return "", err
	}
	if err := client.Respond(target.rpcID, result); err != nil {
		return "", fmt.Errorf("answer Codex: %w", err)
	}
	timer := time.NewTimer(codexResolveWait)
	defer timer.Stop()
	key := codexRPCKey(target.rpcID)
	for {
		for _, event := range client.DrainNotifications() {
			if event.Method == codexRequestResolved && str(event.Params, "threadId") == sess.ID && codexRPCKey(event.Params["requestId"]) == key {
				return outcome, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", fmt.Errorf("no confirmation from Codex of the answer to %s", requestID)
		case <-client.Notified():
		}
	}
}
