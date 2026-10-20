package surfaces

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

var claudePermissionModes = []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}

// hostWaiting is the oldest request the host holds for the session. Its id is
// Claude's own request id, so each request is its own question.
func (c *Claude) hostWaiting(ctx context.Context, sess *surface.Session) *surface.Waiting {
	listed, ok := c.host.session(ctx, sess.ID, sess.PID)
	if !ok || len(listed.Requests) == 0 {
		return nil
	}
	oldest := listed.Requests[0]
	return &surface.Waiting{Kind: claudeRequestKind(oldest.Kind), ID: oldest.RequestID, Since: oldest.CreatedAt.UTC()}
}

// Requests lists the open requests of a session the host runs, oldest first,
// read from the host now rather than from its cached listing.
func (c *Claude) Requests(ctx context.Context, sess *surface.Session) (surface.RequestState, error) {
	state := surface.RequestState{Requests: []surface.Request{}}
	if !claudeHosted(sess) {
		return state, fmt.Errorf("%w: Claude answers requests through Agenthail only in sessions its host runs", surface.ErrUnsupported)
	}
	c.host.forget()
	listed, ok := c.host.session(ctx, sess.ID, sess.PID)
	if !ok {
		return state, nil
	}
	state.PermissionMode = listed.PermissionMode
	for _, request := range listed.Requests {
		state.Requests = append(state.Requests, claudeRequest(request))
	}
	return state, nil
}

// Respond answers one open request of a session the host runs. A request no
// host holds is stale: it was answered or withdrawn, its process ended, or
// its process runs on without a host, which no answer reaches. A request
// lives only in the host that holds Claude's prompt, so none outlives it.
func (c *Claude) Respond(ctx context.Context, sess *surface.Session, requestID string, decision surface.Decision) (string, error) {
	if !claudeHosted(sess) {
		return "", fmt.Errorf("%w: Claude answers requests through Agenthail only in sessions its host runs", surface.ErrUnsupported)
	}
	if requestID == "" {
		return "", fmt.Errorf("a request id is required")
	}
	defer c.host.forget()
	outcome, err := c.host.client.Respond(ctx, sess.ID, requestID, claudehost.Decision{Behavior: decision.Behavior, Always: decision.Always, Message: decision.Message, Answers: decision.Answers})
	switch {
	case claudehost.IsCode(err, "stale"), claudehost.IsCode(err, "not_open"), claudehost.IsCode(err, "held"), errors.Is(err, claudehost.ErrNotRunning):
		return "", fmt.Errorf("%w: %s", surface.ErrRequestStale, err)
	case err != nil:
		return "", claudeHostDeliveryError(err)
	}
	return outcome, nil
}

// hostSetting changes a hosted session's model or permission mode while it
// runs. The host keeps both, so a later resume reopens with them.
func (c *Claude) hostSetting(ctx context.Context, sess *surface.Session, action, value string) (*surface.ControlResult, error) {
	var listed claudehost.Session
	var err error
	switch action {
	case "model":
		listed, err = c.host.client.SetModel(ctx, sess.ID, value)
	case "permission-mode":
		listed, err = c.host.client.SetPermissionMode(ctx, sess.ID, value)
	}
	c.host.forget()
	if err != nil {
		return nil, claudeHostDeliveryError(err)
	}
	if action == "model" {
		sess.ConfiguredModel = listed.Model
		return &surface.ControlResult{SessionID: sess.ID, Action: action, Evidence: surface.EvidenceDelivered, Source: "claude-host", Output: listed.Model}, nil
	}
	return &surface.ControlResult{SessionID: sess.ID, Action: action, Evidence: surface.EvidenceDelivered, Source: "claude-host", Output: listed.PermissionMode}, nil
}

func validClaudePermissionMode(mode string) bool {
	return slices.Contains(claudePermissionModes, mode)
}

func claudeRequestKind(kind string) surface.WaitingKind {
	if kind == "question" {
		return surface.WaitingQuestion
	}
	return surface.WaitingApproval
}

func claudeRequest(request claudehost.Request) surface.Request {
	out := surface.Request{
		ID: request.RequestID, Kind: claudeRequestKind(request.Kind), Tool: request.Tool,
		Title: request.Title, DisplayName: request.DisplayName, Description: request.Description,
		Reason: request.DecisionReason, BlockedPath: request.BlockedPath, AgentID: request.AgentID,
		Input: request.Input, AlwaysAllow: request.AlwaysAllow, CreatedAt: request.CreatedAt.UTC(),
	}
	for _, question := range request.Questions {
		options := make([]surface.QuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, surface.QuestionOption{Label: option.Label, Description: option.Description})
		}
		out.Questions = append(out.Questions, surface.Question{Question: question.Question, Header: question.Header, MultiSelect: question.MultiSelect, Options: options})
	}
	return out
}
