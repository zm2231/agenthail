package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (f *coordinatorFixture) sendToLead(t *testing.T, key string, request map[string]any) (int, map[string]any) {
	t.Helper()
	request["action"] = "coordinator-send"
	if request["name"] == nil {
		request["name"] = "fixture-lead"
	}
	body, _ := json.Marshal(request)
	response := serveV1Action(f.handler, string(body), key, f.token)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Errorf("status=%d body=%s", response.Code, response.Body.String())
	}
	return response.Code, decoded
}

func TestASendToAGoneLeadStartsOneAndMovesWhatTheOldLeadHeld(t *testing.T) {
	f := newCoordinatorFixture(t)
	if err := f.r.QueueMessage(goneCoordinator, "queued before the restart"); err != nil {
		t.Fatal(err)
	}
	const worker = "6a7b8c9d-0e1f-4a2b-8c3d-4e5f6a7b8c9d"
	if err := f.r.RegisterSession(surface.Session{ID: worker, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: orphanPID, Status: surface.StatusIdle, Cwd: f.host.Cwd}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.CreateChannel("team"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.AddToChannel("team", goneCoordinator); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.AddRoute(goneCoordinator, worker, ".*"); err != nil {
		t.Fatal(err)
	}

	code, body := f.sendToLead(t, "lead-gone", map[string]any{"message": "plan the fixture release"})
	current := f.current(t)
	if code != http.StatusOK || body["started"] != true || receiptField(body, "sessionId") != current || current == goneCoordinator || f.started(t) != 1 || f.resumedGone(t) {
		t.Fatalf("status=%d body=%v current=%s started=%d resumed gone=%v", code, body, current, f.started(t), f.resumedGone(t))
	}
	first := f.landed(t, current, "plan the fixture release")[0].Text
	for _, part := range []string{"You coordinate fixture workers.", "conversation " + goneCoordinator + " no longer exists", "plan the fixture release"} {
		if !strings.Contains(first, part) {
			t.Fatalf("first turn %q lacks %q", first, part)
		}
	}
	if f.r.QueueCount(goneCoordinator) != 0 {
		t.Fatalf("the gone lead kept %d queued messages", f.r.QueueCount(goneCoordinator))
	}
	if members, err := f.r.ChannelMembers("team"); err != nil || len(members) != 1 || members[0] != current {
		t.Fatalf("channel members=%v err=%v", members, err)
	}
	if routes, err := f.r.ListRoutes(); err != nil || len(routes) != 1 || routes[0].FromSession != current {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if code, body := f.sendToLead(t, "lead-next", map[string]any{"message": "next task"}); code != http.StatusOK || body["started"] == true || f.started(t) != 1 || f.current(t) != current {
		t.Fatalf("status=%d body=%v started=%d current=%s", code, body, f.started(t), f.current(t))
	}
}

func TestALeadWithNoConversationStartsWithItsModelAndFolder(t *testing.T) {
	f := newCoordinatorFixture(t)
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-fixture", ProjectRoot: f.host.Cwd, Name: "fixture-lead", Surface: string(surface.KindClaude), Cwd: f.host.Cwd, Model: "fixture-model"}, registry.CoordinatorChange{Replace: true}); err != nil {
		t.Fatal(err)
	}
	code, body := f.sendToLead(t, "lead-first", map[string]any{"message": "first task"})
	if code != http.StatusOK || body["started"] != true || !strings.Contains(receiptField(body, "detail"), "Started coordinator @fixture-lead") || receiptField(body, "deliveredAs") != "turn" {
		t.Fatalf("status=%d body=%v", code, body)
	}
	current := f.current(t)
	if first := f.landed(t, current, "first task")[0].Text; !strings.HasSuffix(first, "first task") {
		t.Fatalf("first turn=%q", first)
	}
	started, err := f.r.Session(current)
	if err != nil || started.ConfiguredModel != "fixture-model" {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	spawns := f.host.Spawns(t, current)
	if len(spawns) != 1 || !strings.Contains(strings.Join(spawns[0].Argv, " "), "fixture-model") {
		t.Fatalf("spawns=%+v", spawns)
	}
}

func TestACodexLeadStartsOnItsFirstMessage(t *testing.T) {
	f := newHeadlessCodexFixture(t, unreachableDesktop)
	cwd := t.TempDir()
	if err := f.registry.AddProject(registry.ProjectRecord{ID: "project-codex", WorkspaceRoot: cwd, Title: "fixture", Added: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-codex", ProjectRoot: cwd, Name: "fixture-lead", Surface: string(surface.KindCodex), Cwd: cwd}, registry.CoordinatorChange{}); err != nil {
		t.Fatal(err)
	}
	code, body := f.action(t, map[string]any{"action": "coordinator-send", "name": "fixture-lead", "message": "first task"})
	if code != http.StatusOK || body["started"] != true {
		t.Fatalf("status=%d body=%v", code, body)
	}
	id, err := f.registry.LookupAlias("fixture-lead")
	if err != nil || receiptField(body, "sessionId") != id {
		t.Fatalf("lead=%s err=%v body=%v", id, err, body)
	}
	thread := f.thread(t, id)
	if len(thread.Turns) != 1 || !strings.HasSuffix(thread.Turns[0].User, "first task") || len(f.lifecycle.Daemon().Calls("thread/start")) != 1 {
		t.Fatalf("thread=%+v", thread)
	}
}

func TestANoQueueSendToAGoneLeadStillStartsIt(t *testing.T) {
	f := newCoordinatorFixture(t)
	code, body := f.sendToLead(t, "lead-urgent", map[string]any{"message": "urgent task", "noQueue": true})
	current := f.current(t)
	if code != http.StatusOK || body["started"] != true || f.started(t) != 1 || f.resumedGone(t) {
		t.Fatalf("status=%d body=%v started=%d resumed gone=%v", code, body, f.started(t), f.resumedGone(t))
	}
	if first := f.landed(t, current, "urgent task")[0].Text; !strings.HasSuffix(first, "urgent task") {
		t.Fatalf("first turn=%q", first)
	}
}
