package surface

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tidwall/gjson"
)

func TestAMovedProjectCarriesOnlyItsSettingsAndNoServerCredentials(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude-one")
	writeClaudeJSON(t, root, `{"projects":{"/src/app":{"hasTrustDialogAccepted":true,"allowedTools":["Bash(go test:*)"],"lastCost":3.2,"lastSessionId":"s1","exampleFiles":["a.go"],"mcpServers":{"docs":{"command":"docs-mcp","env":{"TOKEN":"secret"}},"api":{"type":"http","url":"https://example.test","headers":{"Authorization":"Bearer x"}},"plain":{"command":"plain-mcp"}}}}}`)
	entry, stripped, err := ClaudeProjectForMove(home, root, "/src/app")
	if err != nil {
		t.Fatal(err)
	}
	carried := gjson.ParseBytes(entry)
	if !carried.Get("hasTrustDialogAccepted").Bool() || carried.Get("allowedTools.0").String() != "Bash(go test:*)" || carried.Get("mcpServers.plain.command").String() != "plain-mcp" {
		t.Fatalf("settings not carried: %s", entry)
	}
	for _, left := range []string{"lastCost", "lastSessionId", "exampleFiles", "mcpServers.docs.env", "mcpServers.api.headers"} {
		if carried.Get(left).Exists() {
			t.Fatalf("%s left the source: %s", left, entry)
		}
	}
	if carried.Get("mcpServers.docs.command").String() != "docs-mcp" || carried.Get("mcpServers.api.url").String() != "https://example.test" {
		t.Fatalf("a server without its credentials was not kept: %s", entry)
	}
	if !slices.Equal(stripped, []string{"api", "docs"}) {
		t.Fatalf("stripped=%v", stripped)
	}

	none, stripped, err := ClaudeProjectForMove(home, root, "/src/other")
	if err != nil || none != nil || stripped != nil {
		t.Fatalf("a folder with no entry carried %s %v %v", none, stripped, err)
	}
	writeClaudeJSON(t, root, `{"projects":{"/src/app":{"lastCost":1}}}`)
	if none, _, err := ClaudeProjectForMove(home, root, "/src/app"); err != nil || none != nil {
		t.Fatalf("an entry of per-machine state only carried %s %v", none, err)
	}
}

func TestAMovedProjectMergesUnderTheTargetFolderOnceAndUndoesExactly(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude-two")
	target := writeClaudeJSON(t, root, `{"numStartups":3,"projects":{"/srv/checkouts/app":{"hasTrustDialogAccepted":false,"allowedTools":["Edit"],"mcpServers":{"docs":{"command":"kept"}}}}}`)
	before, _ := os.ReadFile(target)
	entry := []byte(`{"hasTrustDialogAccepted":true,"allowedTools":["Bash","Edit"],"mcpServers":{"docs":{"command":"new"},"search":{"command":"search-mcp"}}}`)
	undo, err := MergeMovedClaudeProject(home, root, "/srv/checkouts/app", entry)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	merged := gjson.GetBytes(data, "projects."+escapeSettingsPath("/srv/checkouts/app"))
	if merged.Get("hasTrustDialogAccepted").Bool() || merged.Get("mcpServers.docs.command").String() != "kept" {
		t.Fatalf("the target's own values were overwritten: %s", data)
	}
	if merged.Get("mcpServers.search.command").String() != "search-mcp" || len(merged.Get("allowedTools").Array()) != 2 {
		t.Fatalf("missing settings not added: %s", data)
	}
	again, err := MergeMovedClaudeProject(home, root, "/srv/checkouts/app", entry)
	if err != nil {
		t.Fatal(err)
	}
	if retried, _ := os.ReadFile(target); string(retried) != string(data) {
		t.Fatalf("a repeated merge changed the config: %s", retried)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(target); gjson.GetBytes(after, "projects."+escapeSettingsPath("/srv/checkouts/app")).Raw != gjson.GetBytes(before, "projects."+escapeSettingsPath("/srv/checkouts/app")).Raw || gjson.GetBytes(after, "numStartups").Int() != 3 {
		t.Fatalf("undo left %s, want the entry of %s", after, before)
	}
	if _, err := MergeMovedClaudeProject(home, root, "/srv/checkouts/app", []byte(`["not","an","object"]`)); err == nil {
		t.Fatal("a non-object entry was merged")
	}
}

func TestAProjectReachedThroughASymlinkIsReadAndWrittenUnderItsPhysicalPath(t *testing.T) {
	home := t.TempDir()
	physical := filepath.Join(home, "physical", "app")
	if err := os.MkdirAll(physical, 0700); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(physical)
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(home, "linked")
	if err := os.Symlink(filepath.Join(home, "physical"), linked); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(linked, "app")
	from, to := filepath.Join(home, ".claude-one"), filepath.Join(home, ".claude-two")
	writeClaudeJSON(t, from, `{"projects":{"`+physical+`":{"hasTrustDialogAccepted":true}}}`)
	target := writeClaudeJSON(t, to, `{}`)

	entry, _, err := ClaudeProjectForMove(home, from, cwd)
	if err != nil || !gjson.GetBytes(entry, "hasTrustDialogAccepted").Bool() {
		t.Fatalf("the entry Claude keyed by the physical path was not read through the link: %s %v", entry, err)
	}
	if _, err := MergeMovedClaudeProject(home, to, cwd, entry); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if !gjson.GetBytes(data, "projects."+escapeSettingsPath(physical)+".hasTrustDialogAccepted").Bool() || gjson.GetBytes(data, "projects."+escapeSettingsPath(cwd)).Exists() {
		t.Fatalf("the merge did not key the entry by the physical path: %s", data)
	}

	again := writeClaudeJSON(t, filepath.Join(home, ".claude-three"), `{}`)
	if _, err := CarryClaudeProject(home, from, filepath.Join(home, ".claude-three"), cwd); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(again); !gjson.GetBytes(data, "projects."+escapeSettingsPath(physical)+".hasTrustDialogAccepted").Bool() {
		t.Fatalf("the move between accounts did not carry through the link: %s", data)
	}
}

func TestATargetConfigThatCannotTakeAProjectIsRefused(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude-three")
	if err := CheckClaudeProjectTarget(home, root); err != nil {
		t.Fatalf("a root with no config yet was refused: %v", err)
	}
	writeClaudeJSON(t, root, `{"projects":[]}`)
	if err := CheckClaudeProjectTarget(home, root); err == nil {
		t.Fatal("a projects array was accepted")
	}
	writeClaudeJSON(t, root, `{"projects":`)
	if err := CheckClaudeProjectTarget(home, root); err == nil {
		t.Fatal("an unparsable config was accepted")
	}
}
