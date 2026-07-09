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

func SetChromeProfile(name string) {
	if name != "" {
		chromeProfile = name
	}
}

func sidecarPath() (string, error) {
	if p := os.Getenv("AGENTHAIL_SIDECAR"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"sidecar.py", "claude-worker"} {
			sibling := filepath.Join(filepath.Dir(exe), name)
			if _, err := os.Stat(sibling); err == nil {
				return sibling, nil
			}
		}
	}
	for _, name := range []string{"sidecar.py", "claude-worker"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("sidecar not found (set AGENTHAIL_SIDECAR, install it alongside agenthail, or put it on PATH)")
}

type workerResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

// sidecarPost sends a POST via the sidecar (curl_cffi Chrome TLS impersonation).
// cookieBridge is the path to a .mjs script that prints cookie headers on stdout.
// cookieURL is passed as an arg to the cookie bridge (e.g. "https://claude.ai/" or "https://app.notion.com/").
// Pass "" for either to skip.
func sidecarPostWithCookies(url string, headers map[string]string, body string, cookieBridge string, cookieURL string, timeout time.Duration) (int, string, error) {
	return callSidecar("POST", url, headers, body, cookieBridge, cookieURL, timeout)
}

func callSidecar(method, url string, headers map[string]string, body string, cookieBridge string, cookieURL string, timeout time.Duration) (int, string, error) {
	worker, err := sidecarPath()
	if err != nil {
		return 0, "", err
	}
	req := map[string]any{
		"method":  method,
		"url":     url,
		"headers": headers,
		"timeout": int(timeout.Seconds()),
	}
	if body != "" {
		req["body"] = body
	}
	if cookieBridge != "" {
		req["cookie_bridge"] = cookieBridge
	}
	if cookieURL != "" {
		req["cookie_bridge_args"] = []string{cookieURL}
	}
	if chromeProfile != "" {
		req["profile"] = chromeProfile
	}
	input, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", worker)
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		return 0, "", fmt.Errorf("sidecar failed: %w", err)
	}
	var resp workerResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, "", fmt.Errorf("parse sidecar output: %w (raw: %s)", err, string(out))
	}
	if resp.Error != "" {
		return 0, "", fmt.Errorf("sidecar: %s", resp.Error)
	}
	return resp.Status, resp.Body, nil
}

func cookieBridgePath(name string) string {
	for _, check := range []string{name, name + ".mjs"} {
		if exe, err := os.Executable(); err == nil {
			sibling := filepath.Join(filepath.Dir(exe), check)
			if _, err := os.Stat(sibling); err == nil {
				return sibling
			}
		}
		if p, err := exec.LookPath(check); err == nil {
			return p
		}
	}
	return ""
}
