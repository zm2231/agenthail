package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovalHookDeniesUntilExplicitLocalAllow(t *testing.T) {
	t.Setenv("AGENTHAIL_APPROVAL_DB", filepath.Join(t.TempDir(), "approvals.db"))
	app := &App{}
	hookInput := `{"request_id":"cli-test","source":"claude-code","tool_name":"Bash","tool_input":{"command":"git push"}}`
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
	var denied struct {
		RequestID string `json:"requestId"`
		Decision  string `json:"decision"`
	}
	if err := json.Unmarshal([]byte(first), &denied); err != nil || denied.RequestID != "cli-test" || denied.Decision != "deny" {
		t.Fatalf("response=%q err=%v", first, err)
	}
	if output, err := captureStdout(t, func() error {
		return app.cmdApproval([]string{"allow", "cli-test", "approved"})
	}); err != nil || !strings.Contains(output, "approval allow: cli-test") {
		t.Fatalf("output=%q err=%v", output, err)
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
