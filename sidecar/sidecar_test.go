package sidecar

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type sidecarResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

func fakeCurlCFFI(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	packageDir := filepath.Join(root, "curl_cffi")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	source := `
import os
class Response:
    status_code = 200
    encoding = "utf-8"
    def iter_content(self, chunk_size=65536):
        remaining = int(os.environ.get("FAKE_RESPONSE_BYTES", "0"))
        while remaining:
            size = min(chunk_size, remaining)
            remaining -= size
            yield b"x" * size
    def close(self):
        pass
class Requests:
    def request(self, **kwargs):
        marker = os.environ.get("FAKE_REQUEST_MARKER")
        if marker:
            open(marker, "w").write("requested")
        return Response()
requests = Requests()
`
	if err := os.WriteFile(filepath.Join(packageDir, "__init__.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func runSidecar(t *testing.T, pythonPath string, input string, extraEnv ...string) sidecarResponse {
	t.Helper()
	cmd := exec.Command("python3", "sidecar.py")
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), append([]string{"PYTHONPATH=" + pythonPath}, extraEnv...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sidecar failed: %v: %s", err, output)
	}
	var response sidecarResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatalf("invalid response: %v: %s", err, output)
	}
	return response
}

func TestSidecarRejectsOversizedResponseWhileStreaming(t *testing.T) {
	root := fakeCurlCFFI(t)
	response := runSidecar(t, root, `{"url":"https://example.test","method":"GET"}`, "AGENTHAIL_MAX_RESPONSE_BYTES=1024", "FAKE_RESPONSE_BYTES=2048")
	if response.Status != 0 || !strings.Contains(response.Error, "response exceeded 1024 bytes") || response.Body != "" {
		t.Fatalf("response=%+v", response)
	}
}

func TestSidecarFailsClosedWhenCookieBridgeFails(t *testing.T) {
	root := fakeCurlCFFI(t)
	marker := filepath.Join(t.TempDir(), "requested")
	response := runSidecar(t, root, `{"url":"https://example.test","cookie_bridge":"/does/not/exist.mjs"}`, "FAKE_REQUEST_MARKER="+marker)
	if !strings.Contains(response.Error, "cookie bridge failed") {
		t.Fatalf("response=%+v", response)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("network request occurred after credential acquisition failed")
	}
}

func TestSidecarInvalidRequestIsStructured(t *testing.T) {
	response := runSidecar(t, fakeCurlCFFI(t), `{`)
	if !strings.Contains(response.Error, "invalid request JSON") {
		t.Fatalf("response=%+v", response)
	}
}
