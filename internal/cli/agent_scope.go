package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

type agentScopeRefusal struct {
	Code    string
	Message string
	Rule    string
}

func (r *agentScopeRefusal) Error() string {
	reason := r.Code
	if r.Rule != "" {
		reason += ", rule " + r.Rule
	}
	return fmt.Sprintf("refused (%s): %s", reason, r.Message)
}

var errAgentScopeDaemonDown = errors.New("this command runs as an agent session, whose reach the Agenthail daemon checks, and the daemon is not running; start it with: agenthail daemon start")

// An agent identity only names the sender: it keeps the reach the CLI has without one.
func (a *App) agentEnvironment() bool {
	for _, source := range surface.CallerIdentity {
		if id := os.Getenv(source.Variable); id != "" {
			return source.Surface != "" || !a.namesAgentIdentity(id)
		}
	}
	return false
}

func (a *App) namesAgentIdentity(selector string) bool {
	reg := a.OpenedRegistry()
	if reg == nil {
		return false
	}
	id, err := reg.ResolveTarget(selector)
	if err != nil {
		return false
	}
	_, err = reg.AgentIdentityOf(id)
	return err == nil
}

// --no-queue delivers at once or fails, so it takes the reach of a send now.
func sendMode(args []string) string {
	if hasFlag(args, "--no-queue") {
		return "now"
	}
	return "queue"
}

func (a *App) controlScope(ctx context.Context, sess *surface.Session, remote *remoteTarget) error {
	target := ""
	if remote != nil {
		target = remote.ID
	} else if sess != nil {
		target = sess.ID
	}
	return a.controlScopeOf(ctx, target)
}

// answerScope checks an answer to a session's request; on a paired host the
// owning host then answers only a request routed to this session.
func (a *App) answerScope(ctx context.Context, sess *surface.Session, remote *remoteTarget, decision string) error {
	target := ""
	if remote != nil {
		target = remote.ID
	} else if sess != nil {
		target = sess.ID
	}
	_, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "answer", Target: target, Mode: decision})
	return err
}

func (a *App) controlScopeOf(ctx context.Context, target string) error {
	_, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "control", Target: target})
	return err
}

var gatedActions = []string{
	"project-add", "project-remove", "project-rename", "project-archive", "project-unarchive", "project-link", "project-unlink", "project-setting",
	"coordinator-set", "coordinator-new", "coordinator-clear",
	"resume-after-limit",
	"relay-add", "relay-remove",
	"channel-create", "channel-delete", "channel-rename", "channel-add", "channel-remove",
	"queue-cancel", "queue-clear",
}

type askedPerson struct {
	askID string
}

func (e *askedPerson) Error() string {
	return "asked the person to allow this (" + e.askID + ")"
}

func (a *App) personAction(ctx context.Context, api string, payload map[string]any) error {
	if !a.agentEnvironment() {
		return nil
	}
	if action, _ := payload["action"].(string); !slices.Contains(gatedActions, action) {
		return fmt.Errorf("%s is not a gated action", action)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	reply, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "operator", Payload: &daemon.PersonAction{API: api, Body: body}, AskPerson: a.askPerson, Reason: a.askReason})
	if err != nil {
		return err
	}
	if reply != nil && reply.AskID != "" {
		return &askedPerson{askID: reply.AskID}
	}
	return nil
}

// payloadScope checks a send or control from the body it would run, so a
// cross-project refusal can be asked of the person with --ask-person.
func (a *App) payloadScope(ctx context.Context, action string, payload map[string]any) (*daemon.ScopeReply, error) {
	if !a.agentEnvironment() {
		return nil, nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	target, _ := payload["sessionId"].(string)
	reply, err := a.agentScope(ctx, daemon.ScopeRequest{Action: action, Target: target, Payload: &daemon.PersonAction{API: daemon.PersonActionAPI, Body: body}, AskPerson: a.askPerson, Reason: a.askReason})
	if err != nil {
		return nil, err
	}
	if reply != nil && reply.AskID != "" {
		return nil, &askedPerson{askID: reply.AskID}
	}
	return reply, nil
}

func (a *App) agentCreate(ctx context.Context, payload map[string]any) ([]byte, int, error) {
	caller, err := a.sourceSessionID(ctx, "")
	if err != nil {
		return nil, 0, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return nil, 0, errAgentScopeDaemonDown
	}
	response, err := client.do(ctx, http.MethodPost, daemon.ScopePath, nil, daemon.ScopeRequest{SessionID: caller, Action: "create", Create: encoded}, nil)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, 0, err
	}
	if refusal := scopeRefusalOf(body); refusal != nil {
		return nil, 0, refusal
	}
	return body, response.StatusCode, nil
}

// A person's own terminal has no agent identity, so --from there keeps the person's reach.
func (a *App) agentScope(ctx context.Context, request daemon.ScopeRequest) (*daemon.ScopeReply, error) {
	if !a.agentEnvironment() {
		return nil, nil
	}
	caller, err := a.sourceSessionID(ctx, "")
	if err != nil {
		return nil, err
	}
	request.SessionID = caller
	if a.scopeOverride != nil {
		return a.scopeOverride(request)
	}
	client, err := a.daemonAPI()
	if err != nil {
		return nil, errAgentScopeDaemonDown
	}
	response, err := client.do(ctx, http.MethodPost, daemon.ScopePath, nil, request, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK {
		var reply daemon.ScopeReply
		if err := json.Unmarshal(body, &reply); err != nil {
			return nil, fmt.Errorf("read the daemon's scope answer: %w", err)
		}
		return &reply, nil
	}
	if refusal := scopeRefusalOf(body); refusal != nil {
		return nil, refusal
	}
	return nil, fmt.Errorf("the daemon could not check this session's reach (%d): %s", response.StatusCode, strings.TrimSpace(string(body)))
}

// The daemon marks its scope refusals, so a failed start's reply is never mistaken for one.
func scopeRefusalOf(body []byte) *agentScopeRefusal {
	var refused struct {
		Scope bool `json:"scope"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Detail map[string]any `json:"detail"`
	}
	if json.Unmarshal(body, &refused) != nil || !refused.Scope || refused.Error.Code == "" {
		return nil
	}
	rule, _ := refused.Detail["rule"].(string)
	return &agentScopeRefusal{Code: refused.Error.Code, Message: refused.Error.Message, Rule: rule}
}

func relayEnd(sess *surface.Session, remote *remoteTarget) string {
	if remote != nil {
		return remote.ID
	}
	if sess != nil {
		return sess.ID
	}
	return ""
}
