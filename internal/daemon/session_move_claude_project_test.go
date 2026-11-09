package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func claudeConfigPath(t *testing.T, node *moveNode) string {
	t.Helper()
	roots, err := surface.ClaudeConfigRoots(node.host.Home)
	if err != nil {
		t.Fatal(err)
	}
	return surface.ClaudeGlobalConfigPath(node.host.Home, roots[0])
}

func readClaudeConfig(t *testing.T, node *moveNode) map[string]any {
	t.Helper()
	data, err := os.ReadFile(claudeConfigPath(t, node))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}
	}
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func physicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func setClaudeProject(t *testing.T, node *moveNode, cwd, entry string) {
	t.Helper()
	cwd = physicalPath(t, cwd)
	config := readClaudeConfig(t, node)
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(entry), &value); err != nil {
		t.Fatal(err)
	}
	projects[cwd] = value
	config["projects"] = projects
	data, _ := json.Marshal(config)
	if err := os.WriteFile(claudeConfigPath(t, node), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func claudeProject(t *testing.T, node *moveNode, cwd string) map[string]any {
	t.Helper()
	projects, _ := readClaudeConfig(t, node)["projects"].(map[string]any)
	entry, _ := projects[physicalPath(t, cwd)].(map[string]any)
	return entry
}

const movingProject = `{"hasTrustDialogAccepted":true,"allowedTools":["Bash(go test:*)"],"lastCost":2.5,"mcpServers":{"docs":{"command":"docs-mcp","env":{"DOCS_TOKEN":"secret"}},"plain":{"command":"plain-mcp"}}}`

func TestAMovedClaudeSessionTakesItsProjectSettingsWithoutServerCredentials(t *testing.T) {
	source, target, _, checkout := movePair(t)
	session := startMovable(t, source)
	setClaudeProject(t, source, session.Cwd, movingProject)
	setClaudeProject(t, target, checkout, `{"allowedTools":["Edit"],"mcpServers":{"plain":{"command":"kept-here"}}}`)

	result, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.MCPServersWithoutCredentials, []string{"docs"}) {
		t.Fatalf("servers without credentials %v", result.MCPServersWithoutCredentials)
	}
	entry := claudeProject(t, target, checkout)
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("trust not carried: %v", entry)
	}
	if tools, _ := entry["allowedTools"].([]any); len(tools) != 2 || tools[0] != "Edit" || tools[1] != "Bash(go test:*)" {
		t.Fatalf("allowed tools %v", entry["allowedTools"])
	}
	servers, _ := entry["mcpServers"].(map[string]any)
	docs, _ := servers["docs"].(map[string]any)
	plain, _ := servers["plain"].(map[string]any)
	if docs["command"] != "docs-mcp" || docs["env"] != nil || plain["command"] != "kept-here" {
		t.Fatalf("servers %v", servers)
	}
	if _, kept := entry["lastCost"]; kept {
		t.Fatalf("per-machine state was carried: %v", entry)
	}
}

func TestATargetThatCannotTakeProjectSettingsLeavesTheSessionHere(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	target := newMoveNode(t)
	target.handler = withoutCarries(target.handler)
	source, target, _, checkout := pairMoveNodes(t, newMoveNode(t), target)
	session := startMovable(t, source)
	setClaudeProject(t, source, session.Cwd, movingProject)

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil || !strings.Contains(err.Error(), "update Agenthail on server; it cannot take the project's Claude settings") {
		t.Fatalf("moving to a target that cannot take the settings returned %v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("the session left for %q", owner)
	}
	if entry := claudeProject(t, target, checkout); entry != nil {
		t.Fatalf("the refused move wrote settings on the target: %v", entry)
	}
}

func TestATargetConfigThatCannotTakeTheSettingsRefusesTheMoveBeforeItLeaves(t *testing.T) {
	source, target, _, _ := movePair(t)
	session := startMovable(t, source)
	setClaudeProject(t, source, session.Cwd, movingProject)
	if err := os.WriteFile(claudeConfigPath(t, target), []byte(`{"projects":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil || !strings.Contains(err.Error(), "projects value that is not an object") {
		t.Fatalf("moving to a target whose config cannot take the settings returned %v", err)
	}
	if owner, _ := source.reg.SessionHost(session.ID); owner != "" {
		t.Fatalf("the session left for %q", owner)
	}
	if path := claudeTranscriptIn(t, source.host.Home, session.ID); path == "" {
		t.Fatal("the refused move took the conversation's transcript away")
	}
}

type stagedClaude struct {
	*surfaces.Claude
	reg      *registry.Registry
	failures atomic.Int32
	fail     string
}

func (s *stagedClaude) ReceiveMove(ctx context.Context, in surface.MoveIn, archive io.Reader, admit func() error) (*surface.Session, error) {
	if s.fail == "receive" && s.failures.Add(-1) >= 0 {
		return nil, surface.ErrMoveInPlaced
	}
	session, err := s.Claude.ReceiveMove(ctx, in, archive, admit)
	if err == nil && s.fail == "take" {
		_ = s.reg.SetMoveState(in.MoveID, registry.MoveFailed, "the record failed", registry.MoveReceived)
	}
	return session, err
}

func stagedMoveNode(t *testing.T, fail string, failures int32) (*moveNode, *stagedClaude) {
	t.Helper()
	allowLoopbackHosts(t)
	host := claudehosttest.New(t, "")
	host.Start(t)
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	claude := surfaces.NewClaude(nil, host.Home)
	staged := &stagedClaude{Claude: claude, reg: reg, fail: fail}
	staged.failures.Store(failures)
	d := newTestDaemon(t, reg, []surface.Surface{staged})
	node := &federationNode{daemon: d, reg: reg, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	return &moveNode{federationNode: node, host: host, claude: claude}, staged
}

func TestAMoveTheTargetCannotRecordLeavesItsClaudeConfigAsItWas(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	staged, _ := stagedMoveNode(t, "take", 0)
	source, target, _, checkout := pairMoveNodes(t, newMoveNode(t), staged)
	session := startMovable(t, source)
	setClaudeProject(t, source, session.Cwd, movingProject)
	setClaudeProject(t, target, checkout, `{"allowedTools":["Edit"]}`)
	before, _ := os.ReadFile(claudeConfigPath(t, target))

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	if err == nil {
		t.Fatal("a move the target could not record succeeded")
	}
	if after, _ := os.ReadFile(claudeConfigPath(t, target)); string(after) != string(before) {
		t.Fatalf("the unrecorded move left settings behind:\n%s\nwant\n%s", after, before)
	}
}

func TestARetriedTransferMergesTheProjectSettingsOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	staged, _ := stagedMoveNode(t, "receive", 1)
	source, target, record, checkout := pairMoveNodes(t, newMoveNode(t), staged)
	session := startMovable(t, source)
	setClaudeProject(t, source, session.Cwd, movingProject)
	setClaudeProject(t, target, checkout, `{"allowedTools":["Edit"]}`)
	before, _ := os.ReadFile(claudeConfigPath(t, target))

	_, err := source.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server"})
	var unknown MoveUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("a transfer whose outcome is unknown returned %v", err)
	}
	if after, _ := os.ReadFile(claudeConfigPath(t, target)); string(after) != string(before) {
		t.Fatalf("the unsettled transfer left settings behind:\n%s", after)
	}
	source.daemon.resumeHostMoves(context.Background(), mustPairedHost(t, source, record.ID))
	if owner, _ := source.reg.SessionHost(session.ID); owner != record.ID {
		t.Fatalf("the retried move left the session with %q", owner)
	}
	entry := claudeProject(t, target, checkout)
	if tools, _ := entry["allowedTools"].([]any); len(tools) != 2 || tools[0] != "Edit" || tools[1] != "Bash(go test:*)" {
		t.Fatalf("after the retry the allowed tools are %v", entry["allowedTools"])
	}
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("after the retry the entry is %v", entry)
	}
}
