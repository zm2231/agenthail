package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/hosts"
)

type daemonAPIClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func daemonEndpoint() (string, string, error) {
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return "", "", err
	}
	if !config.Enabled {
		return "", "", errors.New("dashboard is disabled")
	}
	tokenBytes, err := os.ReadFile(daemon.DashboardTokenPath())
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
	if a.daemonAPIOverride != nil {
		return a.daemonAPIOverride()
	}
	return newDaemonAPIClient()
}

func (c *daemonAPIClient) call(ctx context.Context, method, path string, query url.Values, payload any, header http.Header) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(encoded)
	}
	target := strings.TrimRight(c.baseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, 0, err
	}
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("reach the local daemon: %w", err)
	}
	data, err := hosts.ReadResponse(response)
	return data, response.StatusCode, err
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
	header := http.Header{"Idempotency-Key": []string{"cli-" + uuid.NewString()}, daemon.ActionActorHeader: []string{daemon.CLIActor}}
	return c.call(ctx, http.MethodPost, "/api/v1/actions", nil, payload, header)
}
