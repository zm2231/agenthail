package daemon

import (
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestASessionStartedOutsideAgenthailTellsThePersonWhenItStartsOneInTheHighestMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, _, terminal, started := daemonFixture(t)
	if err := r.RecordSessionOrigin(registry.OriginOf(started.ID, "")); err != nil {
		t.Fatal(err)
	}
	told := &toldPerson{}
	d.notifyDesktop = told.notify
	server := mcpServer(t, d)
	outside, inside := mcpSession(t, server, issueMCPToken(t, d, terminal)), mcpSession(t, server, issueMCPToken(t, d, started))

	for session, want := range map[string]string{terminal.ID: "a session Agenthail did not start", started.ID: "the default"} {
		client := outside
		if session == started.ID {
			client = inside
		}
		who, _ := callMCP(t, client, "agenthail_whoami", map[string]any{})
		role, _ := who["role"].(map[string]any)
		if role["source"] != want {
			t.Fatalf("%s holds its role as %v, want %q", session, role["source"], want)
		}
	}

	cwd := t.TempDir()
	if out, refused := callMCP(t, inside, "agenthail_create", map[string]any{"surface": "codex", "cwd": cwd, "message": "work", "permissionMode": "never", "clientRequestId": "inside"}); refused || len(told.told()) != 0 {
		t.Fatalf("a session Agenthail started: refused=%v out=%v told=%q", refused, out, told.told())
	}
	if out, refused := callMCP(t, outside, "agenthail_create", map[string]any{"surface": "codex", "cwd": cwd, "message": "work", "permissionMode": "on-request", "clientRequestId": "lower"}); refused || len(told.told()) != 0 {
		t.Fatalf("a lower mode: refused=%v out=%v told=%q", refused, out, told.told())
	}
	if out, refused := callMCP(t, outside, "agenthail_create", map[string]any{"surface": "codex", "cwd": cwd, "message": "work", "permissionMode": "never", "clientRequestId": "full"}); refused || len(told.told()) != 1 || !strings.Contains(told.told()[0], "never") || !strings.Contains(told.told()[0], cwd) {
		t.Fatalf("the highest mode: refused=%v out=%v told=%q", refused, out, told.told())
	}

	setRoleDecision(t, r, agentroles.Worker, "session-create-full", agentroles.Ask)
	out, refused := callMCP(t, outside, "agenthail_create", map[string]any{"surface": "codex", "cwd": cwd, "message": "work", "permissionMode": "never", "clientRequestId": "asked"})
	detail, _ := out["detail"].(map[string]any)
	if !refused || out["code"] != "out_of_scope" || detail["rule"] != "full_mode" || len(told.told()) != 1 {
		t.Fatalf("set to ask: refused=%v out=%v told=%q", refused, out, told.told())
	}
}
