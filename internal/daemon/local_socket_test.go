package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/registry"
)

func localSocketPost(t *testing.T, request daemonapi.AgentRoleRequest) (int, string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", daemonctl.LocalSocketPath())
	}}}
	body, _ := json.Marshal(request)
	response, err := client.Post("http://agenthail/api/v1/agents", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var answer bytes.Buffer
	_, _ = answer.ReadFrom(response.Body)
	return response.StatusCode, answer.String()
}

func TestAnAgentCannotRaiseItsOwnRoleWhileThePersonsTerminalCan(t *testing.T) {
	d, reg, root, _ := mcpSocketDaemon(t)
	stop, err := d.startLocalSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	registerClaude(t, reg, "claude-one")
	roleOf := func() string {
		t.Helper()
		record, err := reg.Caller("claude-one")
		if err != nil {
			t.Fatal(err)
		}
		return d.callerRole(record, false).Name
	}
	raise := daemonapi.AgentRoleRequest{Op: "assign", Name: agentroles.Operator, Kind: registry.AssignSession, Target: "claude-one"}
	if status, body := localSocketPost(t, raise); status != http.StatusOK || roleOf() != agentroles.Operator {
		t.Fatalf("a process outside every session assigned a role: %d %s, the session holds %s", status, body, roleOf())
	}
	if status, body := localSocketPost(t, daemonapi.AgentRoleRequest{Op: "unassign", Kind: registry.AssignSession, Target: "claude-one"}); status != http.StatusOK {
		t.Fatalf("unassign answered %d %s", status, body)
	}
	agent := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")}
	agent.writeFor(t, os.Getppid(), "claude-one", true)
	for _, request := range []daemonapi.AgentRoleRequest{
		raise,
		{Op: "unassign", Kind: registry.AssignSession, Target: "claude-one"},
		{Op: "put", Name: agentroles.Worker, Role: agentroles.Role{Reach: agentroles.ReachAll}},
	} {
		if status, body := localSocketPost(t, request); status != http.StatusForbidden {
			t.Fatalf("a command inside session claude-one sent %s and got %d %s", request.Op, status, body)
		}
	}
	if role := roleOf(); role != agentroles.Worker {
		t.Fatalf("the session raised itself to %s", role)
	}
	if records, err := reg.AgentRoles(); err != nil || len(records) != 0 {
		t.Fatalf("a refused write left roles %v (%v)", records, err)
	}
}

func TestAnAgentWhoseClaudeRecordIsTornCannotChangeRoles(t *testing.T) {
	d, reg, root, _ := mcpSocketDaemon(t)
	stop, err := d.startLocalSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	registerClaude(t, reg, "claude-one")
	record := filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")
	if err := os.MkdirAll(filepath.Dir(record), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"pid":`+strconv.Itoa(os.Getppid())+`,"sessionId":"claude-o`), 0600); err != nil {
		t.Fatal(err)
	}
	raise := daemonapi.AgentRoleRequest{Op: "assign", Name: agentroles.Operator, Kind: registry.AssignSession, Target: "claude-one"}
	if status, body := localSocketPost(t, raise); status != http.StatusForbidden || !strings.Contains(body, "could not name") {
		t.Fatalf("a command below a Claude process whose record is torn got %d %s", status, body)
	}
	if assignments, err := reg.AgentRoleAssignments(); err != nil || len(assignments) != 0 {
		t.Fatalf("a refused write left assignments %v (%v)", assignments, err)
	}
}
