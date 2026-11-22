package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

type projectScopeFixture struct {
	d         *Daemon
	from      surface.Session
	callerDir string
}

// newProjectScopeFixture publishes a caller and 50 peers, each in its own
// folder, half in the caller's project and half in another, as discovery
// would record them. No folder's identity is cached.
func newProjectScopeFixture(t *testing.T) projectScopeFixture {
	t.Helper()
	d, r, fake, from, _ := daemonFixture(t)
	view, err := d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	oursDir, theirsDir := addLedgerProject(t, r, "project-ours", "ours").dir, addLedgerProject(t, r, "project-theirs", "theirs").dir
	publish := func(session surface.Session) {
		t.Helper()
		if err := os.MkdirAll(session.Cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
		project, err := ResolvePathProject(context.Background(), r, session.Cwd)
		if err != nil || project == nil {
			t.Fatalf("resolve %s: %v %v", session.Cwd, project, err)
		}
		if !d.publishCatalogSession(context.Background(), fake, session, nil, catalogIdentity{Project: project}, "", 0, nil, false, dashconfig.Config{}, view, time.Now().UTC(), nil, nil) {
			t.Fatalf("%s was not published", session.ID)
		}
	}
	from.Cwd = filepath.Join(oursDir, "caller")
	publish(from)
	for i := range 50 {
		root := theirsDir
		if i%2 == 0 {
			root = oursDir
		}
		id := fmt.Sprintf("peer-%02d", i)
		publish(surface.Session{ID: id, Surface: from.Surface, RuntimeKind: from.RuntimeKind, Status: surface.StatusIdle, Cwd: filepath.Join(root, id)})
	}
	return projectScopeFixture{d: d, from: from, callerDir: from.Cwd}
}

// gitCalls puts a git on PATH that logs each call and waits before running
// the real one, and returns the log's path.
func gitCalls(t *testing.T, wait time.Duration) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	stub := t.TempDir()
	calls := filepath.Join(stub, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nsleep %g\nexec %s \"$@\"\n", calls, wait.Seconds(), git)
	if err := os.WriteFile(filepath.Join(stub, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func loggedGitCalls(t *testing.T, calls string) []string {
	t.Helper()
	ran, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(ran)), "\n")
}

var projectScopeCalls = []struct {
	tool string
	args map[string]any
}{
	{"agenthail_sessions", map[string]any{"filter": "project", "limit": 50}},
	{"agenthail_requests", nil},
	{"agenthail_read", map[string]any{"sessionId": "peer-04", "since": 0}},
}

func callProjectScope(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any, listed int) map[string]any {
	t.Helper()
	got := callWithin(t, func() (map[string]any, bool) { return callMCP(t, session, tool, args) }, 10*time.Second, tool)
	if got.refused {
		t.Fatalf("%s was refused: %v", tool, got.out)
	}
	if tool == "agenthail_sessions" {
		if sessions, _ := got.out["sessions"].([]any); len(sessions) != listed {
			t.Fatalf("the project filter listed %d sessions, want %d", len(sessions), listed)
		}
	}
	return got.out
}

func TestMCPProjectScopeRunsNoGitAndAnswersAtOnceWithManySessions(t *testing.T) {
	f := newProjectScopeFixture(t)
	calls := gitCalls(t, 2*time.Second)
	session := mcpSession(t, mcpServer(t, f.d), issueMCPToken(t, f.d, f.from))
	for _, call := range projectScopeCalls {
		started := time.Now()
		callProjectScope(t, session, call.tool, call.args, 26)
		if took := time.Since(started); took > time.Second {
			t.Fatalf("%s took %s with 51 sessions", call.tool, took)
		}
	}
	if ran := loggedGitCalls(t, calls); len(ran) > 0 {
		t.Fatalf("the MCP calls ran git:\n%s", strings.Join(ran, "\n"))
	}
}

func TestACallerDiscoveryHasNotRecordedResolvesOnlyItsOwnFolderOnce(t *testing.T) {
	f := newProjectScopeFixture(t)
	if _, err := f.d.Registry.WriteDB().Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, f.from.ID); err != nil {
		t.Fatal(err)
	}
	calls := gitCalls(t, 0)
	session := mcpSession(t, mcpServer(t, f.d), issueMCPToken(t, f.d, f.from))
	for index, call := range projectScopeCalls {
		before := len(loggedGitCalls(t, calls))
		callProjectScope(t, session, call.tool, call.args, 25)
		ran := loggedGitCalls(t, calls)
		for _, line := range ran[before:] {
			if !strings.HasPrefix(line, "-C "+f.callerDir+" ") {
				t.Fatalf("%s ran git for a folder other than the caller's:\n%s", call.tool, line)
			}
		}
		if index > 0 && len(ran) > before {
			t.Fatalf("%s resolved the caller's folder again:\n%s", call.tool, strings.Join(ran[before:], "\n"))
		}
	}
	if len(loggedGitCalls(t, calls)) == 0 {
		t.Fatal("the caller's cold folder was never resolved; the test no longer covers a cold caller")
	}
}
