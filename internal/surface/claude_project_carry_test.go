package surface

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func writeClaudeJSON(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, ".claude.json")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCarryClaudeProjectCopiesTheEntryAndUndoRemovesIt(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	cwd := "/work/my.app"
	writeClaudeJSON(t, from, `{"projects":{"/work/my.app":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"],"mcpServers":{"docs":{"command":"docs-mcp"}}}}}`)
	target := writeClaudeJSON(t, to, `{"numStartups":3,"projects":{"/elsewhere":{"hasTrustDialogAccepted":false}}}`)
	undo, err := CarryClaudeProject(home, from, to, cwd)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	entry := gjson.GetBytes(data, "projects."+escapeSettingsPath(cwd))
	if !entry.Get("hasTrustDialogAccepted").Bool() || entry.Get("mcpServers.docs.command").String() != "docs-mcp" || entry.Get("allowedTools.0").String() != "Bash" {
		t.Fatalf("entry not carried: %s", data)
	}
	if gjson.GetBytes(data, "numStartups").Int() != 3 || !gjson.GetBytes(data, `projects./elsewhere`).Exists() {
		t.Fatalf("other config changed: %s", data)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	if gjson.GetBytes(data, "projects."+escapeSettingsPath(cwd)).Exists() || !gjson.GetBytes(data, `projects./elsewhere`).Exists() {
		t.Fatalf("undo left %s", data)
	}
}

func TestCarryClaudeProjectNeverOverwritesTheTargetsEntry(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	cwd := "/work/app"
	writeClaudeJSON(t, from, `{"projects":{"/work/app":{"hasTrustDialogAccepted":true,"allowedTools":["Bash","Edit"],"mcpServers":{"docs":{"command":"new"},"search":{"command":"search-mcp"}}}}}`)
	target := writeClaudeJSON(t, to, `{"projects":{"/work/app":{"hasTrustDialogAccepted":false,"allowedTools":["Edit"],"mcpServers":{"docs":{"command":"kept"}}}}}`)
	undo, err := CarryClaudeProject(home, from, to, cwd)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	entry := gjson.GetBytes(data, "projects./work/app")
	if entry.Get("hasTrustDialogAccepted").Bool() || entry.Get("mcpServers.docs.command").String() != "kept" {
		t.Fatalf("target value overwritten: %s", data)
	}
	if entry.Get("mcpServers.search.command").String() != "search-mcp" || entry.Get("allowedTools").Raw != `["Edit","Bash"]` {
		t.Fatalf("missing parts not added: %s", data)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	entry = gjson.GetBytes(data, "projects./work/app")
	if entry.Get("mcpServers.search").Exists() || entry.Get("allowedTools").Raw != `["Edit"]` || entry.Get("mcpServers.docs.command").String() != "kept" {
		t.Fatalf("undo did not restore the target: %s", data)
	}
}

func TestUndoingACarryKeepsWhatClaudeWroteAfterIt(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	cwd := "/work/a.b*c?d"
	writeClaudeJSON(t, from, `{"projects":{"/work/a.b*c?d":{"hasTrustDialogAccepted":true,"mcpServers":{"docs":{"command":"docs-mcp"}}}}}`)
	target := writeClaudeJSON(t, to, `{"numStartups":1}`)
	undo, err := CarryClaudeProject(home, from, to, cwd)
	if err != nil {
		t.Fatal(err)
	}
	key := "projects." + escapeSettingsPath(cwd)
	data, _ := os.ReadFile(target)
	if gjson.GetBytes(data, key+".mcpServers.docs.command").String() != "docs-mcp" {
		t.Fatalf("entry for a path with special characters not carried: %s", data)
	}
	data, _ = sjson.SetBytes(data, "projects./other.hasTrustDialogAccepted", true)
	data, _ = sjson.SetBytes(data, key+".lastCost", 1.5)
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	if !gjson.GetBytes(data, "projects./other.hasTrustDialogAccepted").Bool() || gjson.GetBytes(data, key+".lastCost").Float() != 1.5 {
		t.Fatalf("undo removed what Claude wrote after the carry: %s", data)
	}
	if gjson.GetBytes(data, key+".hasTrustDialogAccepted").Exists() || gjson.GetBytes(data, key+".mcpServers").Exists() {
		t.Fatalf("undo left what the carry added: %s", data)
	}
}

func TestCarryClaudeProjectLeavesNonObjectEntriesAlone(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	writeClaudeJSON(t, from, `{"projects":{"/work/app":{"hasTrustDialogAccepted":true}}}`)
	for _, body := range []string{`{"projects":{"/work/app":"odd"}}`, `{"projects":[]}`} {
		target := writeClaudeJSON(t, to, body)
		if _, err := CarryClaudeProject(home, from, to, "/work/app"); err != nil {
			t.Fatal(err)
		}
		if data, _ := os.ReadFile(target); string(data) != body {
			t.Fatalf("a carry changed %s into %s", body, data)
		}
	}
	writeClaudeJSON(t, from, `{"projects":{"/work/app":{}}}`)
	target := writeClaudeJSON(t, to, `{"a":1}`)
	if _, err := CarryClaudeProject(home, from, to, "/work/app"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != `{"a":1}` {
		t.Fatalf("an empty entry was carried: %s", data)
	}
}

func TestCarryClaudeProjectWithoutASourceEntryChangesNothing(t *testing.T) {
	home := t.TempDir()
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	writeClaudeJSON(t, from, `{"projects":{}}`)
	target := writeClaudeJSON(t, to, `{"a":1}`)
	undo, err := CarryClaudeProject(home, from, to, "/work/app")
	if err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != `{"a":1}` {
		t.Fatalf("target changed: %s", data)
	}
}
