package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

var chromeProfile = "Default"

// SetChromeProfile sets the Chrome profile name used for cookie loading in the
// sidecar. Called once at startup from main.
func SetChromeProfile(name string) {
	if name != "" {
		chromeProfile = name
	}
}

// workerPath resolves the claude-worker sidecar binary. Order: env override,
// sibling of the agenthail binary, then PATH lookup.
func workerPath() (string, error) {
	if p := os.Getenv("AGENTHAIL_CLAUDE_WORKER"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "claude-worker")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	if p, err := exec.LookPath("claude-worker"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("claude-worker sidecar not found (set AGENTHAIL_CLAUDE_WORKER, install it alongside agenthail, or put it on PATH)")
}

type workerResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

// cyclePost sends a POST via the claude-worker sidecar (curl_cffi, byte-exact
// Chrome TLS impersonation). Required because Claude's edge blocks every pure
// Go TLS library via an anti-bot lottery.
func cyclePost(url string, headers map[string]string, body string, timeout time.Duration) (int, string, error) {
	return callWorker("POST", url, headers, body, timeout)
}

func callWorker(method, url string, headers map[string]string, body string, timeout time.Duration) (int, string, error) {
	worker, err := workerPath()
	if err != nil {
		return 0, "", err
	}
	req := map[string]any{
		"method":  method,
		"url":     url,
		"headers": headers,
		"timeout": int(timeout.Seconds()),
		"profile": chromeProfile,
	}
	if body != "" {
		req["body"] = body
	}
	input, _ := json.Marshal(req)

	ctx := timeoutCtx(timeout + 10*time.Second)
	cmd := exec.CommandContext(ctx, worker)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		return 0, "", fmt.Errorf("claude-worker failed: %w", err)
	}
	var resp workerResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, "", fmt.Errorf("parse claude-worker output: %w (raw: %s)", err, string(out))
	}
	if resp.Error != "" {
		return 0, "", fmt.Errorf("claude-worker: %s", resp.Error)
	}
	return resp.Status, resp.Body, nil
}

func timeoutCtx(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	_ = cancel
	return ctx
}
