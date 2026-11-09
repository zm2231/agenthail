package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

func agentEnvironment() bool {
	for _, source := range surface.CallerIdentity {
		if os.Getenv(source.Variable) != "" {
			return true
		}
	}
	return false
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

func (a *App) controlScopeOf(ctx context.Context, target string) error {
	_, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "control", Target: target})
	return err
}

func (a *App) operatorScope(ctx context.Context, operation string) error {
	_, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "operator", Operation: operation})
	return err
}

func (a *App) projectOperatorScope(ctx context.Context, operation, channel string, sessions ...string) error {
	_, err := a.agentScope(ctx, daemon.ScopeRequest{Action: "operator", Operation: operation, Channel: channel, Sessions: sessions})
	return err
}

func (a *App) queueManageScope(ctx context.Context, op, ref string) error {
	if !agentEnvironment() {
		return nil
	}
	var sessions []string
	if op == "clear" {
		id, err := a.queueSessionID(ctx, ref)
		if err != nil {
			return err
		}
		sessions = []string{id}
	} else if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		item, err := a.registry().QueueItem(id, a.adapters())
		if err != nil {
			return err
		}
		sessions = []string{item.SessionID}
		if item.SourceSessionID != "" {
			sessions = append(sessions, item.SourceSessionID)
		}
	}
	return a.projectOperatorScope(ctx, "Managing the delivery queue beyond your own project", "", sessions...)
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

func (a *App) relayRemoveScope(id int64, operation string) error {
	if !agentEnvironment() {
		return nil
	}
	routes, err := a.registry().ListRoutes()
	if err != nil {
		return err
	}
	var sessions []string
	for _, route := range routes {
		if route.ID == id {
			sessions = []string{route.FromSession, route.ToSession}
		}
	}
	return a.projectOperatorScope(context.Background(), operation, "", sessions...)
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
	if !agentEnvironment() {
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
