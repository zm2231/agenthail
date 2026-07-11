package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/approval"
)

func TestApprovalHookReturnsExplicitLocalAllow(t *testing.T) {
	t.Setenv("AGENTHAIL_APPROVAL_DB", filepath.Join(t.TempDir(), "approvals.db"))
	app := &App{}
	hookInput := `{"request_id":"cli-test","source":"claude-code","tool_name":"Bash","tool_input":{"command":"git push"}}`
	store, err := approval.Open(approval.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	request, err := approval.Normalize([]byte(hookInput), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Observe(request, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Decide(request.ID, approval.DecisionAllow, "test approval", time.Now()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(hookInput); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	os.Stdin = reader
	first, firstErr := captureStdout(t, func() error { return app.cmdApproval([]string{"hook"}) })
	os.Stdin = oldStdin
	reader.Close()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	var allowed struct {
		RequestID string `json:"requestId"`
		Decision  string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(first), &allowed); err != nil || allowed.RequestID != "cli-test" || allowed.Decision != "allow" {
		t.Fatalf("response=%q err=%v", first, err)
	}
}

func TestApprovalHookMalformedInputFailsClosed(t *testing.T) {
	t.Setenv("AGENTHAIL_APPROVAL_DB", filepath.Join(t.TempDir(), "approvals.db"))
	app := &App{}
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.WriteString("not-json")
	writer.Close()
	os.Stdin = reader
	output, runErr := captureStdout(t, func() error { return app.cmdApproval([]string{"hook"}) })
	os.Stdin = oldStdin
	reader.Close()
	if runErr == nil || !strings.Contains(runErr.Error(), "invalid approval request JSON") {
		t.Fatalf("err=%v", runErr)
	}
	var denied struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(output), &denied); err != nil || denied.Decision != "deny" || denied.Reason == "" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestApprovalHookInstallIsIdempotentAndPreservesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","hooks":{"PermissionRequest":[{"hooks":[{"type":"command","command":"existing"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if err := app.cmdApproval([]string{"install"}); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(first), "approval hook") || !strings.Contains(string(first), "existing") {
		t.Fatalf("settings=%q err=%v", first, err)
	}
	if err := app.cmdApproval([]string{"install"}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if strings.Count(string(second), "approval hook") != 1 {
		t.Fatalf("duplicate hook settings=%q", second)
	}
	if err := app.cmdApproval([]string{"uninstall"}); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(path)
	if strings.Contains(string(third), "approval hook") || !strings.Contains(string(third), "existing") {
		t.Fatalf("uninstall settings=%q", third)
	}
}

func TestApprovalHookStoreFailureStillReturnsDeny(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTHAIL_APPROVAL_DB", dir)
	app := &App{}
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writer.WriteString(`{"tool_name":"Bash"}`)
	writer.Close()
	os.Stdin = reader
	output, runErr := captureStdout(t, func() error { return app.cmdApproval([]string{"hook"}) })
	os.Stdin = oldStdin
	reader.Close()
	if runErr == nil || !strings.Contains(runErr.Error(), "approval broker unavailable") {
		t.Fatalf("err=%v", runErr)
	}
	var denied struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(output), &denied); err != nil || denied.Decision != "deny" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestApprovalHookInstallRejectsMalformedHookShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installApprovalHook(); err == nil || !strings.Contains(err.Error(), "hooks must be an object") {
		t.Fatalf("err=%v", err)
	}
}

func TestApprovalListAcceptsCountAndJSON(t *testing.T) {
	t.Setenv("AGENTHAIL_APPROVAL_DB", filepath.Join(t.TempDir(), "approvals.db"))
	app := &App{}
	output, err := captureStdout(t, func() error { return app.cmdApproval([]string{"list", "10", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
