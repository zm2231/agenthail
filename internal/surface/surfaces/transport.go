package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

const maxDiagnosticBytes = 2048

func diagnosticExcerpt(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxDiagnosticBytes {
		return value
	}
	cut := value[:maxDiagnosticBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return fmt.Sprintf("%s... [truncated; %d bytes total]", strings.TrimSpace(cut), len(value))
}

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

func sidecarPython() (string, error) {
	candidates := []string{}
	if configured := os.Getenv("AGENTHAIL_PYTHON"); configured != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates, "python3.14", "python3.13", "python3.12", "python3.11", "python3.10", "python3")
	}
	var failures []string
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil {
			failures = append(failures, candidate+": not found")
			continue
		}
		if err := exec.Command(path, "-c", "import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)").Run(); err != nil {
			version, _ := exec.Command(path, "--version").CombinedOutput()
			failures = append(failures, fmt.Sprintf("%s: %s", path, strings.TrimSpace(string(version))))
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("Python 3.10+ is required for the sidecar (set AGENTHAIL_PYTHON; checked %s)", strings.Join(failures, "; "))
}

type workerResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

func sidecarRequestWithCookies(parent context.Context, method, url string, headers map[string]string, body string, cookieBridge string, cookieURL string, timeout time.Duration) (int, string, error) {
	worker, err := sidecarPath()
	if err != nil {
		return 0, "", err
	}
	python, err := sidecarPython()
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

	ctx, cancel := context.WithTimeout(parent, timeout+10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, worker)
	cmd.Stdin = strings.NewReader(string(input))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return 0, "", ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = detail[:500] + "..."
		}
		if detail != "" {
			return 0, "", fmt.Errorf("sidecar failed: %w (%s)", err, detail)
		}
		return 0, "", fmt.Errorf("sidecar failed: %w", err)
	}
	var resp workerResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		raw := stdout.String()
		if len(raw) > 500 {
			raw = raw[:500] + "..."
		}
		return 0, "", fmt.Errorf("parse sidecar output: %w (raw: %s)", err, raw)
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
