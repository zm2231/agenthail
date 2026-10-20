package surface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCarryClaudeProjectConfigAddsTrustApprovalsAndMCPServersToTheTargetRoot(t *testing.T) {
	home := claudeAccountHome(t, "spare")
	spare := filepath.Join(home, "spare")
	cwd := "/work/app"
	source := `{"numStartups":3,"projects":{"/work/app":{"hasTrustDialogAccepted":true,"allowedTools":["Bash(ls)"],"enabledMcpjsonServers":["db"],"mcpServers":{"local-a":{"command":"a"},"shared":{"command":"s"}}}}}`
	target := `{
  "zeta": 1,
  "alpha": {"keep": [1, 2]},
  "projects": {
    "/other": {"hasTrustDialogAccepted": false},
    "/work/app": {"allowedTools": ["Read"], "mcpServers": {"shared": {"command": "mine"}}}
  },
  "oauthAccount": {"accountUuid": "x"}
}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(spare, ".claude.json")
	if err := os.WriteFile(targetPath, []byte(target), 0600); err != nil {
		t.Fatal(err)
	}
	var added []string
	adder := func(_ context.Context, root, dir, name string, config json.RawMessage) error {
		if root != spare || dir != cwd {
			t.Errorf("mcp add ran in root %s cwd %s", root, dir)
		}
		added = append(added, name+"="+string(config))
		return nil
	}
	if err := CarryClaudeProjectConfig(context.Background(), home, filepath.Join(home, ".claude"), spare, cwd, adder); err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != `local-a={"command":"a"}` {
		t.Fatalf("added=%v, want only the server the target lacks", added)
	}
	data, _ := os.ReadFile(targetPath)
	text := string(data)
	if strings.Index(text, `"zeta"`) > strings.Index(text, `"alpha"`) || strings.Index(text, `"alpha"`) > strings.Index(text, `"projects"`) {
		t.Fatalf("key order changed:\n%s", text)
	}
	var config struct {
		Projects map[string]struct {
			Trust      bool                       `json:"hasTrustDialogAccepted"`
			Allowed    []string                   `json:"allowedTools"`
			Enabled    []string                   `json:"enabledMcpjsonServers"`
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
		Alpha struct {
			Keep []int `json:"keep"`
		} `json:"alpha"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	app := config.Projects[cwd]
	if !app.Trust || strings.Join(app.Allowed, ",") != "Read,Bash(ls)" || strings.Join(app.Enabled, ",") != "db" || !strings.Contains(strings.Join(strings.Fields(string(app.MCPServers["shared"])), ""), `"command":"mine"`) {
		t.Fatalf("app=%+v", app)
	}
	if config.Projects["/other"].Trust || len(config.Alpha.Keep) != 2 {
		t.Fatalf("an unrelated entry changed: %s", text)
	}
	if info, _ := os.Stat(targetPath); info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}

	before, _ := os.Stat(targetPath)
	if err := CarryClaudeProjectConfig(context.Background(), home, filepath.Join(home, ".claude"), spare, cwd, func(context.Context, string, string, string, json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(targetPath); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("a carry with nothing new rewrote the file")
	}
}

func TestUpdateClaudeGlobalConfigRetriesWhenClaudeWritesMeanwhile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := updateClaudeGlobalConfig(path, func(config *orderedObject) bool {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte(`{"a":1,"claude":"wrote this"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		config.set("agenthail", json.RawMessage(`true`))
		return true
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "wrote this") || !strings.Contains(string(data), `"agenthail": true`) {
		t.Fatalf("a concurrent write was lost: %s", data)
	}
}
