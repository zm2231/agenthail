package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/hosts"
)

type daemonAPIClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	forward    *forwardedAction
}

// forwardedAction is the target an agent's scope check named, the limits
// the host that owns it checks, and whether that host delivered a steer to
// it as a message, which the notice the command sends afterwards says.
type forwardedAction struct {
	limits          *daemonapi.ScopeReply
	source          string
	notSteered      *bool
	controlDecision string
	ran             string
}

func daemonEndpoint() (string, string, error) {
	config, err := dashconfig.Load()
	if err != nil {
		return "", "", err
	}
	if !config.Enabled {
		return "", "", errors.New("the dashboard is disabled; turn it on with: agenthail dashboard enable")
	}
	tokenBytes, err := os.ReadFile(dashconfig.TokenPath())
	if err != nil {
		return "", "", fmt.Errorf("read dashboard token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return "", "", errors.New("dashboard token is empty")
	}
	return "http://" + config.Listen, token, nil
}

func newDaemonAPIClient() (*daemonAPIClient, error) {
	baseURL, token, err := daemonEndpoint()
	if err != nil {
		return nil, err
	}
	return &daemonAPIClient{baseURL: baseURL, token: token, httpClient: &http.Client{}}, nil
}

func (a *App) daemonAPI() (*daemonAPIClient, error) {
	if !a.daemonIsRunning() {
		return nil, errors.New("the daemon is not running; start it with: agenthail daemon start")
	}
	open := newDaemonAPIClient
	if a.daemonAPIOverride != nil {
		open = a.daemonAPIOverride
	}
	client, err := open()
	if client != nil {
		client.forward = &a.forwarded
	}
	return client, err
}

// localDaemonAPI reaches the daemon on its local socket, where it names the
// calling process and refuses the person's writes from inside a session.
func (a *App) localDaemonAPI() (*daemonAPIClient, error) {
	if !a.daemonIsRunning() {
		return nil, errors.New("the daemon is not running; start it with: agenthail daemon start")
	}
	if a.daemonAPIOverride != nil {
		return a.daemonAPIOverride()
	}
	path := daemonctl.LocalSocketPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the daemon serves no local socket at %s; a daemon from an older build has none, so update Agenthail and restart the daemon", path)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	return &daemonAPIClient{baseURL: "http://agenthail", httpClient: &http.Client{Transport: transport}}, nil
}

func (c *daemonAPIClient) call(ctx context.Context, method, path string, query url.Values, payload any, header http.Header) ([]byte, int, error) {
	response, err := c.do(ctx, method, path, query, payload, header)
	if err != nil {
		return nil, 0, err
	}
	data, err := hosts.ReadResponse(response)
	return data, response.StatusCode, err
}

func (c *daemonAPIClient) do(ctx context.Context, method, path string, query url.Values, payload any, header http.Header) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	target := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("reach the local daemon: %w", err)
	}
	return response, nil
}

func (c *daemonAPIClient) getJSON(ctx context.Context, path string, query url.Values, value any) error {
	data, _, err := c.call(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// action posts one dashboard action with a fresh idempotency key, so the
// daemon can hold and replay it toward a paired host without applying it
// twice.
func (c *daemonAPIClient) action(ctx context.Context, payload map[string]any) ([]byte, int, error) {
	scoped := c.scope(payload)
	header := http.Header{"Idempotency-Key": []string{"cli-" + uuid.NewString()}, daemonapi.ActionActorHeader: []string{daemonapi.CLIActor}}
	data, status, err := c.call(ctx, http.MethodPost, "/api/v1/actions", nil, payload, header)
	if scoped && err == nil {
		c.recordOutcome(data)
	}
	return data, status, err
}

// actionReceipt is action keeping the body of a refusal, whose fields can
// carry a partial outcome.
func (c *daemonAPIClient) actionReceipt(ctx context.Context, payload map[string]any) ([]byte, int, error) {
	scoped := c.scope(payload)
	header := http.Header{"Idempotency-Key": []string{"cli-" + uuid.NewString()}, daemonapi.ActionActorHeader: []string{daemonapi.CLIActor}}
	response, err := c.do(ctx, http.MethodPost, "/api/v1/actions", nil, payload, header)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if scoped && err == nil && response.StatusCode < 300 {
		c.recordOutcome(data)
	}
	return data, response.StatusCode, err
}

// scope carries the limits the daemon named for this command's target on
// the action for that target, so the host that owns it checks them.
func (c *daemonAPIClient) scope(payload map[string]any) bool {
	if c.forward == nil {
		return false
	}
	limits := c.forward.limits
	if target, _ := payload["sessionId"].(string); limits == nil || target != limits.TargetID || payload["scopeProjectId"] != nil {
		return false
	}
	c.forward.ran, _ = payload["action"].(string)
	if limits.ScopeProjectID == "" {
		return false
	}
	payload["scopeProjectId"] = limits.ScopeProjectID
	if limits.ScopeRank != "" {
		payload["scopeRank"] = limits.ScopeRank
	}
	if limits.ScopeControl != "" {
		payload["scopeOverseen"], payload["scopeControl"] = limits.ScopeOverseen, limits.ScopeControl
		if source, _ := payload["sourceSessionId"].(string); source == "" && c.forward.source != "" {
			payload["sourceSessionId"] = c.forward.source
		}
	}
	return true
}

func (c *daemonAPIClient) recordOutcome(data []byte) {
	var reply struct {
		Result struct {
			NotSteered *bool `json:"notSteered"`
		} `json:"result"`
		ControlDecision string `json:"controlDecision"`
	}
	if json.Unmarshal(data, &reply) == nil {
		c.forward.notSteered, c.forward.controlDecision = reply.Result.NotSteered, reply.ControlDecision
	}
}
