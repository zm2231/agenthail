package daemon

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type headlessCodexFixture struct {
	registry  *registry.Registry
	lifecycle *codextest.Lifecycle
	handler   http.Handler
	dropTurns atomic.Bool
}

func newHeadlessCodexFixture(t *testing.T, desktopURL string) *headlessCodexFixture {
	t.Helper()
	fixture := &headlessCodexFixture{}
	home := codextest.Home(t)
	fixture.lifecycle = codextest.InstallLifecycle(t, home, func(method string, _ map[string]any) map[string]any {
		if method == "turn/start" && fixture.dropTurns.Load() {
			return codextest.Drop
		}
		return nil
	})
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fixture.registry = r
	fixture.restartAgenthail(t, desktopURL)
	return fixture
}

func (f *headlessCodexFixture) restartAgenthail(t *testing.T, desktopURL string) {
	f.handler = dashboardRouter(newTestDaemon(t, f.registry, []surface.Surface{surfaces.NewCodex(desktopURL)}))
}

func (f *headlessCodexFixture) action(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	response := serveDashboardRequest(f.handler, http.MethodPost, "/api/action", string(data))
	var decoded map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	if decoded == nil {
		decoded = map[string]any{"text": response.Body.String()}
	}
	return response.Code, decoded
}

func (f *headlessCodexFixture) thread(t *testing.T, id string) codextest.Thread {
	t.Helper()
	thread, ok := f.lifecycle.Daemon().Thread(id)
	if !ok {
		t.Fatalf("daemon has no thread %s", id)
	}
	return thread
}

const unreachableDesktop = "1"

func TestHeadlessCodexDashboardCreatesAndDrivesAThreadOnTheDaemon(t *testing.T) {
	fixture := newHeadlessCodexFixture(t, unreachableDesktop)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "Build the release notes", "cwd": t.TempDir()})
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if id == "" || session["transport"] != "managed" {
		t.Fatalf("created=%v", created)
	}
	if commands := fixture.lifecycle.Commands(); len(commands) != 1 || commands[0] != "app-server daemon start" {
		t.Fatalf("lifecycle commands=%v, want one start", commands)
	}
	if thread := fixture.thread(t, id); len(thread.Turns) != 1 || thread.Turns[0].User != "Build the release notes" || thread.Turns[0].Status != "inProgress" {
		t.Fatalf("thread=%+v", thread)
	}
	if starts := fixture.lifecycle.Daemon().Calls("thread/start"); len(starts) != 1 || starts[0]["threadSource"] != "agenthail" {
		t.Fatalf("thread/start=%v", starts)
	}

	stored, err := fixture.registry.Session(id)
	if err != nil || stored.Transport != "managed" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	listed, err := surfaces.NewCodex(unreachableDesktop).List(t.Context())
	if err != nil || len(listed) != 1 || listed[0].ID != id || listed[0].Transport != "managed" {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}

	if code, body := fixture.action(t, map[string]any{"action": "steer", "sessionId": id, "message": "Keep it short"}); code != http.StatusOK {
		t.Fatalf("steer status=%d body=%v", code, body)
	}
	if thread := fixture.thread(t, id); !strings.Contains(thread.Turns[0].User, "Keep it short") {
		t.Fatalf("steer did not reach the active turn: %+v", thread)
	}
	if code, body := fixture.action(t, map[string]any{"action": "interrupt", "sessionId": id}); code != http.StatusOK {
		t.Fatalf("interrupt status=%d body=%v", code, body)
	}
	if thread := fixture.thread(t, id); thread.Turns[0].Status != "interrupted" {
		t.Fatalf("thread=%+v", thread)
	}
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Now finish it"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	if thread := fixture.thread(t, id); len(thread.Turns) != 2 || thread.Turns[1].User != "Now finish it" {
		t.Fatalf("thread=%+v", thread)
	}
	if commands := fixture.lifecycle.Commands(); len(commands) != 1 {
		t.Fatalf("a running daemon was started again: %v", commands)
	}
}

func TestHeadlessCodexSendAfterAgenthailAndDaemonRestartsResumesTheThread(t *testing.T) {
	fixture := newHeadlessCodexFixture(t, unreachableDesktop)
	code, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First task", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if code >= 300 || id == "" {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")

	fixture.lifecycle.Daemon().Unload(id)
	fixture.restartAgenthail(t, unreachableDesktop)
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "After an Agenthail restart"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")

	fixture.lifecycle.Crash()
	fixture.restartAgenthail(t, unreachableDesktop)
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "After a daemon crash"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	thread := fixture.thread(t, id)
	if len(thread.Turns) != 3 || thread.Turns[2].User != "After a daemon crash" {
		t.Fatalf("thread=%+v", thread)
	}
	if resumes := fixture.lifecycle.Daemon().Calls("thread/resume"); len(resumes) != 1 || resumes[0]["threadId"] != id {
		t.Fatalf("restarted daemon resumes=%v", resumes)
	}
	if commands := fixture.lifecycle.Commands(); len(commands) != 2 {
		t.Fatalf("lifecycle commands=%v, want a start for creation and one after the crash", commands)
	}
}

func TestHeadlessCodexUncertainSendStaysUnknown(t *testing.T) {
	fixture := newHeadlessCodexFixture(t, unreachableDesktop)
	_, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "First task", "cwd": t.TempDir()})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	fixture.lifecycle.Daemon().CompleteTurn(id, "done")
	fixture.dropTurns.Store(true)
	code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": id, "message": "Lost on the wire"})
	result, _ := body["result"].(map[string]any)
	if code != http.StatusOK || result["status"] != "submitted" || result["evidence"] != "submitted" {
		t.Fatalf("send status=%d body=%v, want the unknown outcome recorded as submitted", code, body)
	}
	history, err := fixture.registry.ListHistory(20, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.Message == "Lost on the wire" && entry.Kind != "submitted" {
			t.Fatalf("uncertain write recorded as %q", entry.Kind)
		}
	}
}

func TestHeadlessCodexDesktopCreatedThreadStaysOnDesktop(t *testing.T) {
	const desktopThread = "019f004a-0000-7000-8000-0000000d0001"
	desktopRead := `{"result":{"thread":{"id":"` + desktopThread + `","originator":"Codex Desktop","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true}}}`
	desktop := codextest.StartDesktop(t, func(method string) string {
		switch method {
		case "thread/loaded/list":
			return `{"result":{"data":["` + desktopThread + `"]}}`
		case "thread/read":
			return desktopRead
		case "thread/turns/list":
			return `{"result":{"data":[]}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
	fixture := newHeadlessCodexFixture(t, desktop.URL)
	daemon := fixture.lifecycle.Start(t)
	daemon.AddThread(codextest.Thread{ID: desktopThread, Originator: "Codex Desktop"})
	if err := fixture.registry.RegisterSession(surface.Session{ID: desktopThread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}); err != nil {
		t.Fatal(err)
	}
	if code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": desktopThread, "message": "Through Desktop"}); code != http.StatusOK {
		t.Fatalf("send status=%d body=%v", code, body)
	}
	if !strings.Contains(desktop.Request("turn/start"), "Through Desktop") {
		t.Fatalf("Desktop methods=%v", desktop.Methods())
	}
	for _, method := range []string{"thread/resume", "turn/start"} {
		if calls := daemon.Calls(method); len(calls) != 0 {
			t.Fatalf("the daemon received %s for a Desktop thread", method)
		}
	}

	fixture.restartAgenthail(t, unreachableDesktop)
	code, body := fixture.action(t, map[string]any{"action": "send", "sessionId": desktopThread, "message": "Desktop is closed"})
	encoded, _ := json.Marshal(body)
	if code == http.StatusOK || !strings.Contains(string(encoded), "agenthail launch codex") {
		t.Fatalf("send status=%d body=%s, want read only until Desktop returns", code, encoded)
	}
	if daemon.Loaded(desktopThread) || len(daemon.Calls("thread/resume")) != 0 {
		t.Fatalf("the daemon loaded a Desktop thread: %v", daemon.Methods())
	}
}

func TestDashboardRecordsWhichSessionStartedAThread(t *testing.T) {
	fixture := newHeadlessCodexFixture(t, unreachableDesktop)
	if err := fixture.registry.RegisterSession(surface.Session{ID: "planner-conversation", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
		t.Fatal(err)
	}
	if code, _ := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir(), "sourceSessionId": "not-a-session"}); code != http.StatusBadRequest {
		t.Fatalf("an unknown creator status=%d", code)
	}
	for _, source := range []string{"planner-conversation", ""} {
		_, created := fixture.action(t, map[string]any{"action": "session-create", "surface": "codex", "message": "Build", "cwd": t.TempDir(), "sourceSessionId": source})
		session, _ := created["session"].(map[string]any)
		id, _ := session["id"].(string)
		origin, found, err := fixture.registry.SessionOrigin(id)
		want := registry.OriginOf(id, source)
		if err != nil || !found || origin != want {
			t.Fatalf("source %q: origin=%+v found=%v err=%v, want %+v", source, origin, found, err, want)
		}
	}
}
