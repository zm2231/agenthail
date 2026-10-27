package cliproxyapi

import (
	"bytes"
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
	status, body, err := c.send(ctx, http.MethodGet, "/healthz", "", nil)
	if err != nil {
		return &proxyacct.Error{Problem: proxyacct.Unreachable, Err: err}
	}
	if status != http.StatusOK || json.Unmarshal(body, &answer) != nil || answer.Status != "ok" {
		return &proxyacct.Error{Problem: proxyacct.Unreachable, Err: fmt.Errorf("health check answered %d", status)}
	}
	return nil
}

func (c *Client) Accounts(ctx context.Context) ([]proxyacct.Account, error) {
	body, err := c.manage(ctx, http.MethodGet, "/v0/management/auth-files", nil)
	if err != nil {
		return nil, err
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

// A refused key is not offered again until it changes, and a ban is waited
// out, so polling never adds to the proxy's failed-attempt count.
func (c *Client) manage(ctx context.Context, method, path string, payload any) ([]byte, error) {
	key, err := c.key.Read(ctx)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(key))
	if err := c.paused(sum); err != nil {
		return nil, err
	}
	status, body, err := c.send(ctx, method, path, key, payload)
	if err != nil {
		return nil, &proxyacct.Error{Problem: proxyacct.Unreachable, Err: err}
	}
	if status != http.StatusOK {
		return nil, c.refusal(path, status, body, sum)
	}
	return body, nil
}

func (c *Client) refusal(path string, status int, body []byte, sum [sha256.Size]byte) error {
	var answer struct {
		Error string `json:"error"`
	}
	json.Unmarshal(body, &answer)
	message := strings.ToLower(answer.Error)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case status == http.StatusNotFound && strings.Contains(message, "auth file not found"):
		return &proxyacct.Error{Problem: proxyacct.NoAccount, Err: errors.New("the proxy has no such account")}
	case status == http.StatusForbidden && (strings.Contains(message, "read-only") || strings.Contains(message, "read_only")):
		return &proxyacct.Error{Problem: proxyacct.ReadOnly, Err: errors.New("this key can read the proxy but not change it")}
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
	if answer.Error != "" {
		return &proxyacct.Error{Problem: proxyacct.UnknownAnswer, Err: fmt.Errorf("%s answered %d: %s", path, status, answer.Error)}
	}
	return &proxyacct.Error{Problem: proxyacct.UnknownAnswer, Err: fmt.Errorf("%s answered %d", path, status)}
}

func (c *Client) send(ctx context.Context, method, path, key string, payload any) (int, []byte, error) {
	var content io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		content = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.url+path, content)
	if err != nil {
		return 0, nil, err
	}
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
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
