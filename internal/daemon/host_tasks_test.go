package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type hostTaskFixture struct {
	local, remote *federationNode
	record        registry.PairedHost
	parent        *mcp.ClientSession
	taskID        int64
}

func startHostTask(t *testing.T) hostTaskFixture {
	t.Helper()
	local, remote, record := pairedFederation(t)
	caller, err := local.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	parent := mcpSession(t, mcpServer(t, local.daemon), issueMCPToken(t, local.daemon, *caller))
	out, refused := callMCP(t, parent, "agenthail_create", map[string]any{"surface": "codex", "host": "vps", "cwd": t.TempDir(), "message": "build the parser", "task": true, "clientRequestId": "remote-task"})
	if refused {
		t.Fatalf("create a task on the paired host: %v", out)
	}
	taskID, _ := out["taskId"].(float64)
	if taskID == 0 {
		t.Fatalf("create answered without a taskId: %v", out)
	}
	held, found, err := remote.reg.TaskForChild("started")
	if err != nil || !found || held.State != registry.TaskRunning {
		t.Fatalf("the paired host holds the task: %+v found=%v err=%v", held, found, err)
	}
	return hostTaskFixture{local: local, remote: remote, record: record, parent: parent, taskID: int64(taskID)}
}

func (f hostTaskFixture) result(t *testing.T) map[string]any {
	t.Helper()
	out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.taskID})
	if refused {
		t.Fatalf("result: %v", out)
	}
	return out
}

func TestATaskStartedOnAPairedHostSettlesWhenItsChildReportsThere(t *testing.T) {
	f := startHostTask(t)
	if running, err := f.local.reg.RunningChildren("local-a"); err != nil || len(running) != 1 || running[0] != "started" {
		t.Fatalf("running=%v err=%v, want the remote child to hold one of the spawner's slots", running, err)
	}
	waited := make(chan map[string]any, 1)
	go func() {
		out, _ := callMCP(t, f.parent, "agenthail_wait", map[string]any{"tasks": []string{fmt.Sprint(f.taskID)}, "timeoutSec": 20})
		waited <- out
	}()
	child, err := f.remote.reg.Session("started")
	if err != nil {
		t.Fatal(err)
	}
	reporter := mcpSession(t, mcpServer(t, f.remote.daemon), issueMCPToken(t, f.remote.daemon, *child))
	if out, refused := callMCP(t, reporter, "agenthail_report", map[string]any{"status": "done", "result": "the parser is built", "clientRequestId": "report"}); refused {
		t.Fatalf("report on the paired host: %v", out)
	}
	select {
	case out := <-waited:
		if fmt.Sprint(out["timedOut"]) == "true" {
			t.Fatalf("wait timed out instead of returning the remote settlement: %v", out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("wait did not return")
	}
	out := f.result(t)
	if out["state"] != registry.TaskDone || out["kind"] != registry.TaskKindReported || out["result"] != "the parser is built" {
		t.Fatalf("result=%v, want the child's report", out)
	}
	if running, err := f.local.reg.RunningChildren("local-a"); err != nil || len(running) != 0 {
		t.Fatalf("running=%v err=%v, want the settled task's slot freed", running, err)
	}
}

func TestATaskOnAPairedHostThatEndsWhileTheLinkIsDownSettlesOnReconnect(t *testing.T) {
	f := startHostTask(t)
	f.remote.gate.proxyDown.Store(true)
	f.remote.server.CloseClientConnections()
	testkit.Eventually(t, "the paired host seen as down", 10*time.Second, func() bool {
		host, _ := f.local.daemon.pairedHost(f.record.ID)
		state, _ := host.status()
		return state != hostStateConnected
	})
	child, err := f.remote.reg.Session("started")
	if err != nil {
		t.Fatal(err)
	}
	observation := &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("parsed every file")}
	f.remote.fake.observations[child.ID] = observation
	f.remote.daemon.settleTaskFromObservation(context.Background(), f.remote.fake, child, observation)
	if held, _, _ := f.remote.reg.TaskForChild(child.ID); held.State != registry.TaskDone {
		t.Fatalf("the paired host settles its own task: %+v", held)
	}
	out := f.result(t)
	if out["state"] != registry.TaskRunning || out["next"] == nil {
		t.Fatalf("result while the host is down=%v, want running and why", out)
	}
	if running, _ := f.local.reg.RunningChildren("local-a"); len(running) != 1 {
		t.Fatalf("running=%v, want the slot held while the host is unreachable", running)
	}
	f.remote.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the task settled after the host reconnects", 20*time.Second, func() bool {
		task, _, _ := f.local.reg.TaskForChild(child.ID)
		return task.State == registry.TaskDone
	})
	if out := f.result(t); out["kind"] != registry.TaskKindTurn || out["result"] != "parsed every file" {
		t.Fatalf("result=%v, want the child's last turn", out)
	}
}
