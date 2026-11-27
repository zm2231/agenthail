package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/daemonapi"
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
	return startHostTaskWith(t, func(t *testing.T, f hostTaskFixture) any {
		out, refused := callMCP(t, f.parent, "agenthail_create", map[string]any{"surface": "codex", "host": "vps", "cwd": t.TempDir(), "message": "build the parser", "task": true, "clientRequestId": "remote-task"})
		if refused {
			t.Fatalf("create a task on the paired host: %v", out)
		}
		return out["taskId"]
	})
}

func startHostTaskWith(t *testing.T, create func(*testing.T, hostTaskFixture) any) hostTaskFixture {
	t.Helper()
	local, remote, record := pairedFederation(t)
	caller, err := local.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	f := hostTaskFixture{local: local, remote: remote, record: record}
	f.parent = mcpSession(t, mcpServer(t, local.daemon), issueMCPToken(t, local.daemon, *caller))
	taskID, _ := create(t, f).(float64)
	if taskID == 0 {
		t.Fatal("create answered without a taskId")
	}
	f.taskID = int64(taskID)
	if task := f.localTask(t); task.ID != f.taskID || task.Parent != "local-a" || task.State != registry.TaskRunning {
		t.Fatalf("task here=%+v, want task %d of local-a running", task, f.taskID)
	}
	held, found, err := remote.reg.TaskForChild("started")
	if err != nil || !found || held.State != registry.TaskRunning {
		t.Fatalf("the paired host holds the task: %+v found=%v err=%v", held, found, err)
	}
	child, err := remote.reg.Session("started")
	if err != nil {
		t.Fatal(err)
	}
	f.reporter = mcpSession(t, mcpServer(t, remote.daemon), issueMCPToken(t, remote.daemon, *child))
	return f
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
		out, _ := callMCP(t, f.parent, "agenthail_wait", map[string]any{"tasks": []string{fmt.Sprint(f.taskID)}, "timeoutSec": 25})
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
	case <-time.After(10 * time.Second):
		t.Fatal("wait did not return when the task settled")
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

func TestAnAgentsShellStartsATaskOnAPairedHostThroughTheCLI(t *testing.T) {
	f := startHostTaskWith(t, func(t *testing.T, f hostTaskFixture) any {
		payload, _ := json.Marshal(map[string]any{"action": "session-create", "host": "vps", "surface": "codex", "message": "build the parser", "cwd": t.TempDir(), "sourceSessionId": "local-a", "task": true})
		answer := askScope(t, f.local.daemon, daemonapi.ScopeRequest{SessionID: "local-a", Action: "create", Create: payload}, "127.0.0.1:50000")
		var created map[string]any
		if answer.status >= http.StatusMultipleChoices || json.Unmarshal(answer.body, &created) != nil {
			t.Fatalf("create through the CLI's scope request: status=%d body=%s", answer.status, answer.body)
		}
		return created["taskId"]
	})
	waited := f.wait(t)
	f.report(t, "built from the shell")
	settledByWait(t, waited)
	if out := f.result(t); out["state"] != registry.TaskDone || out["result"] != "built from the shell" {
		t.Fatalf("result=%v, want the child's report", out)
	}
}

func TestATaskOnAPairedHostNeedsASessionHereToStartIt(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "host": "vps", "surface": "codex", "message": "orphan", "cwd": t.TempDir(), "task": true})
	if response.Code < http.StatusBadRequest || !strings.Contains(response.Body.String(), "the session on this computer that started it") {
		t.Fatalf("status=%d body=%s, want a task with no starting session refused here", response.Code, response.Body.String())
	}
	if _, err := remote.reg.Session("started"); err == nil {
		t.Fatal("the paired host started a child for a task nothing here can wait on")
	}
}

func TestARetriedRemoteTaskIsNotRecordedHereOnceItsParentMovedAway(t *testing.T) {
	local, remote, record := pairedFederation(t)
	body, _ := json.Marshal(map[string]any{"action": "session-create", "host": "vps", "surface": "codex", "cwd": t.TempDir(), "message": "build the parser", "task": true, "sourceSessionId": "local-a"})
	create := func() actionReply {
		return local.daemon.performAction(context.Background(), sessionPrincipal("local-a"), actionFormatAPIv1, body, "lost-task")
	}
	remote.gate.drops.Store(2)
	if lost := create(); lost.Status != http.StatusGatewayTimeout {
		t.Fatalf("a create whose answer was lost = %d %s", lost.Status, lost.Body)
	}
	remote.gate.drops.Store(0)
	db, err := sql.Open("sqlite", local.reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET host_id=? WHERE id='local-a'`, record.ID); err != nil {
		t.Fatal(err)
	}
	retry := create()
	if retry.Status != http.StatusConflict || !strings.Contains(string(retry.Body), "task_parent_moved") {
		t.Fatalf("the retry after the parent moved = %d %s, want it told the task is not kept here", retry.Status, retry.Body)
	}
	if running, _ := local.reg.RunningChildren("local-a"); len(running) != 0 {
		t.Fatalf("a task was recorded here for a parent that moved: %v", running)
	}
}

func TestARetriedRemoteTaskTheHostIsStillStartingStaysOpen(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	body, _ := json.Marshal(map[string]any{"action": "session-create", "host": "vps", "surface": "codex", "cwd": t.TempDir(), "message": "build the parser", "task": true, "sourceSessionId": "local-a"})
	create := func() actionReply {
		return local.daemon.performAction(context.Background(), sessionPrincipal("local-a"), actionFormatAPIv1, body, "slow-task")
	}
	remote.gate.drops.Store(2)
	if lost := create(); lost.Status != http.StatusGatewayTimeout {
		t.Fatalf("a create whose answer was lost = %d %s", lost.Status, lost.Body)
	}
	remote.gate.drops.Store(0)
	db, err := sql.Open("sqlite", remote.reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT idempotency_key FROM action_receipts WHERE idempotency_key LIKE '%slow-task'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE action_receipts SET status='pending', receipt=x'' WHERE idempotency_key=?`, stored); err != nil {
		t.Fatal(err)
	}
	length, rest, _ := strings.Cut(stored, ":")
	size, _ := strconv.Atoi(length)
	remote.daemon.actionRuns.mu.Lock()
	remote.daemon.actionRuns.running = map[string]bool{actionRunID(rest[:size], rest[size:]): true}
	remote.daemon.actionRuns.mu.Unlock()
	if still := create(); still.Status != http.StatusAccepted {
		t.Fatalf("a retry while the host is still starting it = %d %s, want it still submitted", still.Status, still.Body)
	}
	receipt, found, err := local.reg.ActionReceipt(sessionPrincipal("local-a").ID, "slow-task")
	if err != nil || !found || receipt.Status != registry.ActionReceiptRetry {
		t.Fatalf("receipt here=%+v found=%v err=%v, want it open for the next retry", receipt, found, err)
	}
}
