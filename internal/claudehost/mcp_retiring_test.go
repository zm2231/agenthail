package claudehost_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
)

func TestResumingAConversationARetiringHostHoldsHandsThatHostTheNewCredential(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	f.Open(t, client, sessionID)
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "SLOW 60000"}); err != nil {
		t.Fatal(err)
	}
	if client, err = f.Manager(t, "2").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if retiring := client.Retiring(); len(retiring) != 1 {
		t.Fatalf("retiring hosts = %d, want the one holding the conversation", len(retiring))
	}
	opened, err := client.Open(ctx, claudehost.OpenRequest{Resume: sessionID, Cwd: f.Cwd, MCP: &claudehost.MCPServer{URL: "http://127.0.0.1:7412/mcp", Token: "ahs_replacement"}})
	if err != nil || opened.SessionID != sessionID || opened.MCPError != "" {
		t.Fatalf("open = %s %q, err %v", opened.SessionID, opened.MCPError, err)
	}
	data, err := os.ReadFile(filepath.Join(f.ClaudeDir, "controls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	swapped := false
	for _, line := range claudehosttest.Lines(data) {
		var control struct {
			SessionID string `json:"sessionId"`
			Subtype   string `json:"subtype"`
			Servers   map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"servers"`
		}
		if json.Unmarshal(line, &control) == nil && control.SessionID == sessionID && control.Subtype == "mcp_set_servers" {
			swapped = control.Servers["agenthail"].Headers["Authorization"] == "Bearer ahs_replacement"
		}
	}
	if !swapped {
		t.Fatal("the retiring host's Claude never got the new credential")
	}
	if err := client.Interrupt(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
}
