package cliproxyapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

const (
	maxAnswerBytes = 8 << 20
	banPause       = 30 * time.Minute
)

type Client struct {
	url  string
	key  *proxyacct.KeyRef
	http *http.Client
	now  func() time.Time

	mu          sync.Mutex
	refusedKey  [sha256.Size]byte
	refused     bool
	bannedUntil time.Time
}

func New(url string, key *proxyacct.KeyRef, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{url: strings.TrimRight(url, "/"), key: key, http: client, now: time.Now}
}

func (c *Client) Health(ctx context.Context) error {
	var answer struct {
		Status string `json:"status"`
	}
	status, body, err := c.get(ctx, "/healthz", "")
	if err != nil {
		return &proxyacct.Error{Problem: proxyacct.Unreachable, Err: err}
	}
	if status != http.StatusOK || json.Unmarshal(body, &answer) != nil || answer.Status != "ok" {
		return &proxyacct.Error{Problem: proxyacct.Unreachable, Err: fmt.Errorf("health check answered %d", status)}
	}
	return nil
}

// A refused key is not offered again until it changes, and a ban is waited
// out, so polling never adds to the proxy's failed-attempt count.
func (c *Client) Accounts(ctx context.Context) ([]proxyacct.Account, error) {
	key, err := c.key.Read(ctx)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(key))
	if err := c.paused(sum); err != nil {
		return nil, err
	}
	status, body, err := c.get(ctx, "/v0/management/auth-files", key)
	if err != nil {
		return nil, &proxyacct.Error{Problem: proxyacct.Unreachable, Err: err}
	}
	if status != http.StatusOK {
		return nil, c.refusal(status, body, sum)
	}
	var answer authFiles
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, &proxyacct.Error{Problem: proxyacct.UnknownAnswer, Err: fmt.Errorf("auth-files answer is not the expected JSON: %w", err)}
	}
	return answer.accounts(c.now()), nil
}

func (c *Client) paused(sum [sha256.Size]byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refused && c.refusedKey == sum {
		return &proxyacct.Error{Problem: proxyacct.KeyRefused, Err: errors.New("the proxy refused this management key; it is not offered again until it changes")}
	}
	c.refused = false
	if until := c.bannedUntil; c.now().Before(until) {
		return &proxyacct.Error{Problem: proxyacct.Banned, Err: fmt.Errorf("the proxy blocked this host after failed key attempts; retrying after %s", until.Local().Format("15:04"))}
	}
	return nil
}

func (c *Client) refusal(status int, body []byte, sum [sha256.Size]byte) error {
	var answer struct {
		Error string `json:"error"`
	}
	json.Unmarshal(body, &answer)
	message := strings.ToLower(answer.Error)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case status == http.StatusNotFound || strings.Contains(message, "key not set"):
		return &proxyacct.Error{Problem: proxyacct.NoManagement, Err: errors.New("the proxy has no management key set")}
	case strings.Contains(message, "remote management disabled"):
		return &proxyacct.Error{Problem: proxyacct.RemoteOff, Err: errors.New("the proxy does not allow management from this host")}
	case strings.Contains(message, "banned"):
		c.bannedUntil = c.now().Add(banPause)
		return &proxyacct.Error{Problem: proxyacct.Banned, Err: errors.New("the proxy blocked this host after failed key attempts")}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		c.refused, c.refusedKey = true, sum
		return &proxyacct.Error{Problem: proxyacct.KeyRefused, Err: fmt.Errorf("the proxy refused the management key (%d)", status)}
	}
	return &proxyacct.Error{Problem: proxyacct.UnknownAnswer, Err: fmt.Errorf("auth-files answered %d", status)}
}

func (c *Client) get(ctx context.Context, path, key string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path, nil)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, redact(err, key)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAnswerBytes))
	return response.StatusCode, body, redact(err, key)
}

func redact(err error, key string) error {
	if err == nil || key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "[key]"))
}
