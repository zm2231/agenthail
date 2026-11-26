package daemon

import (
	"context"
	"fmt"
	"net/http"
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
	reporter      *mcp.ClientSession
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
	child, err := remote.reg.Session("started")
	if err != nil {
		t.Fatal(err)
	}
	reporter := mcpSession(t, mcpServer(t, remote.daemon), issueMCPToken(t, remote.daemon, *child))
	return hostTaskFixture{local: local, remote: remote, record: record, parent: parent, reporter: reporter, taskID: int64(taskID)}
}

func (f hostTaskFixture) result(t *testing.T) map[string]any {
	t.Helper()
	out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.taskID})
	if refused {
		t.Fatalf("result: %v", out)
	}
	return out
}

func (f hostTaskFixture) report(t *testing.T, result string) {
	t.Helper()
	if out, refused := callMCP(t, f.reporter, "agenthail_report", map[string]any{"status": "done", "result": result, "clientRequestId": "report-" + result}); refused {
		t.Fatalf("report on the paired host: %v", out)
	}
}

func (f hostTaskFixture) wait(t *testing.T) <-chan map[string]any {
	t.Helper()
	waited := make(chan map[string]any, 1)
	go func() {
		out, _ := callMCP(t, f.parent, "agenthail_wait", map[string]any{"tasks": []string{fmt.Sprint(f.taskID)}, "timeoutSec": 20})
		waited <- out
	}()
	return waited
}

func settledByWait(t *testing.T, waited <-chan map[string]any) {
	t.Helper()
	select {
	case out := <-waited:
		ready, _ := out["ready"].([]any)
		if len(ready) != 1 || ready[0].(map[string]any)["type"] != "settled" {
			t.Fatalf("wait=%v, want the remote settlement ready", out)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("wait did not return")
	}
}

func (f hostTaskFixture) linkDown(t *testing.T) {
	t.Helper()
	f.remote.gate.proxyDown.Store(true)
	f.remote.server.CloseClientConnections()
	testkit.Eventually(t, "the paired host seen as down", 10*time.Second, func() bool {
		host, _ := f.local.daemon.pairedHost(f.record.ID)
		state, _ := host.status()
		return state != hostStateConnected
	})
}

func (f hostTaskFixture) localTask(t *testing.T) registry.Task {
	t.Helper()
	task, found, err := f.local.reg.TaskForChild("started")
	if err != nil || !found {
		t.Fatalf("task here: found=%v err=%v", found, err)
	}
	return task
}

func TestATaskStartedOnAPairedHostSettlesWhenItsChildReportsThere(t *testing.T) {
	f := startHostTask(t)
	if running, err := f.local.reg.RunningChildren("local-a"); err != nil || len(running) != 1 || running[0] != "started" {
		t.Fatalf("running=%v err=%v, want the remote child to hold one of the spawner's slots", running, err)
	}
	waited := f.wait(t)
	f.report(t, "the parser is built")
	settledByWait(t, waited)
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
	f.linkDown(t)
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
		return f.localTask(t).State == registry.TaskDone
	})
	if out := f.result(t); out["kind"] != registry.TaskKindTurn || out["result"] != "parsed every file" {
		t.Fatalf("result=%v, want the child's last turn", out)
	}
}

func TestALaterReportMadeWhileTheLinkIsDownReachesTheSpawnerOnReconnect(t *testing.T) {
	f := startHostTask(t)
	f.report(t, "first pass done")
	testkit.Eventually(t, "the first report settles the task here", 10*time.Second, func() bool {
		return f.localTask(t).State == registry.TaskDone
	})
	f.linkDown(t)
	f.report(t, "second pass done")
	if task := f.localTask(t); task.Latest != "" {
		t.Fatalf("latest=%q before the host reconnects", task.Latest)
	}
	f.remote.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the later report reaches the spawner's host", 20*time.Second, func() bool {
		task := f.localTask(t)
		return task.Latest == "second pass done" && task.Result == "first pass done"
	})
}

func TestArchivingATasksChildOnItsHostEndsTheWaitOfItsSpawner(t *testing.T) {
	f := startHostTask(t)
	waited := f.wait(t)
	if response := f.remote.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "session-archive", "sessionId": "started"}); response.Code != http.StatusOK {
		t.Fatalf("archive on the paired host: status=%d body=%s", response.Code, response.Body.String())
	}
	settledByWait(t, waited)
	if task := f.localTask(t); task.State != registry.TaskCancelled {
		t.Fatalf("task here=%+v, want cancelled", task)
	}
	if running, _ := f.local.reg.RunningChildren("local-a"); len(running) != 0 {
		t.Fatalf("running=%v, want the cancelled task's slot freed", running)
	}
}
