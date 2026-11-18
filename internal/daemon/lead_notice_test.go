package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type noticeFixture struct {
	d       *Daemon
	fake    *daemonSurface
	asking  *requestingSurface
	reg     *registry.Registry
	repo    string
	worker  surface.Session
	request surface.Request
	waiting surface.Waiting
}

func newNoticeFixture(t *testing.T) noticeFixture {
	t.Helper()
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	repo := widgetRepo(t, "https://github.com/example/widget.git")
	sessions := map[string]surface.Session{}
	for _, id := range []string{"worker", "lead", "spawner"} {
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: repo, LastActive: time.Now()}
		if err := reg.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
		sessions[id] = session
	}
	if err := reg.SetAlias("builder", "worker"); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordSessionOrigin(registry.OriginOf("worker", "spawner")); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{sessions: sessions, observations: map[string]*surface.TurnObservation{}, accepted: true}
	request := surface.Request{ID: "approve-1", Kind: surface.WaitingApproval, Tool: "Bash", Description: "rm -rf build"}
	asking := &requestingSurface{daemonSurface: fake, open: []surface.Request{request}}
	d := newTestDaemon(t, reg, []surface.Surface{asking})
	waiting := surface.Waiting{Kind: surface.WaitingApproval, ID: "approve-1", Answerable: true}
	return noticeFixture{d: d, fake: fake, asking: asking, reg: reg, repo: repo, worker: sessions["worker"], request: request, waiting: waiting}
}

func (f noticeFixture) setLead(t *testing.T, sessionID string) {
	t.Helper()
	spec := registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: f.repo, Name: "widget-lead", Surface: "codex", Cwd: f.repo}
	if _, err := f.reg.SetCoordinator(spec, registry.CoordinatorChange{SessionID: sessionID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
}

func (f noticeFixture) notices(t *testing.T) []registry.QueueRow {
	t.Helper()
	rows, err := f.reg.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var notices []registry.QueueRow
	for _, row := range rows {
		if row.SourceSessionID == surface.SystemSessionID && strings.HasPrefix(row.Message, "[agenthail] "+f.d.resolveDisplay(f.worker.ID)+" ") {
			notices = append(notices, row)
		}
	}
	return notices
}

func (f noticeFixture) tell(t *testing.T, d *Daemon, session surface.Session, waiting surface.Waiting) registry.Ask {
	t.Helper()
	d.syncWaits(session, &waiting)
	d.background.wait()
	ask, found := askOfRequest(f.reg, session.ID, waiting.ID)
	if !found {
		t.Fatalf("no ask for %s", waiting.ID)
	}
	return ask
}

func TestAWorkersPermissionPromptGoesToItsLeadOnceWithHowToAnswer(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	ask := f.tell(t, f.d, f.worker, f.waiting)
	f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].SessionID != "lead" || notices[0].Status != "pending" || ask.RoutedTo != "lead" {
		t.Fatalf("ask=%+v notices=%+v", ask, notices)
	}
	for _, want := range []string{"Bash: rm -rf build", "Read the request first: agenthail requests @builder --json", "agenthail answer @builder", "--request " + ask.ID, "Or pass it to the person: agenthail ask escalate " + ask.ID, "within 3 minutes"} {
		if !strings.Contains(notices[0].Message, want) {
			t.Fatalf("the notice %q does not say %q", notices[0].Message, want)
		}
	}
}

func TestALeadIsToldTheCommandAWorkersPromptAsksToRun(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.asking.open = []surface.Request{{ID: "approve-1", Kind: surface.WaitingApproval, Tool: "Bash", Input: json.RawMessage(`{"command":"docker system prune -af","description":"free disk"}`)}}
	f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Message, "blocked on a prompt: Bash\ndocker system prune -af\n") {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestALongCommandTakesOneLineOfTheNotice(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	command := "cat > notes.md <<'EOF'\n" + strings.Repeat("a long line of the heredoc\n", 400) + "EOF"
	input, _ := json.Marshal(map[string]string{"command": command})
	f.asking.open = []surface.Request{{ID: "approve-1", Kind: surface.WaitingApproval, Tool: "Bash", Input: input}}
	f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 {
		t.Fatalf("notices=%+v", notices)
	}
	_, rest, _ := strings.Cut(notices[0].Message, "blocked on a prompt: Bash\n")
	line, _, _ := strings.Cut(rest, "\n")
	if !strings.HasPrefix(line, "cat > notes.md <<'EOF' ⏎ a long line") || !strings.HasSuffix(line, " …") || len(line) > noticeDetailLimit+len(" …") {
		t.Fatalf("the command took %q, want one clipped line", line)
	}
}

func TestALeadWithAgenthailToolsIsToldTheToolsToAnswerWith(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	if _, err := f.d.Registry.IssueSessionCredential("lead", time.Now()); err != nil {
		t.Fatal(err)
	}
	ask := f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 {
		t.Fatalf("notices=%+v", notices)
	}
	for _, want := range []string{`agenthail_requests {"sessionId":"@builder","requestId":"` + ask.ID + `"}`, `agenthail_answer {"requestId":"` + ask.ID + `","sessionId":"@builder","decision":"allow|deny"}`, `agenthail_answer {"requestId":"` + ask.ID + `","sessionId":"@builder","decision":"escalate"}`} {
		if !strings.Contains(notices[0].Message, want) {
			t.Fatalf("the notice %q does not say %q", notices[0].Message, want)
		}
	}
	if strings.Contains(notices[0].Message, "agenthail answer ") {
		t.Fatalf("a lead with tools is told the CLI: %q", notices[0].Message)
	}
}

func TestAPromptGoesToTheSpawnerWhenTheProjectHasNoLead(t *testing.T) {
	f := newNoticeFixture(t)
	f.tell(t, f.d, f.worker, f.waiting)
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestALeadsOwnPromptGoesToItsSpawner(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "worker")
	f.tell(t, f.d, f.worker, f.waiting)
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestAPromptWithNoLeadAndNoSpawnerGoesToThePerson(t *testing.T) {
	f := newNoticeFixture(t)
	orphan := f.worker
	orphan.ID = "lead"
	ask := f.tell(t, f.d, orphan, f.waiting)
	rows, err := f.reg.ListQueue(true, nil)
	if err != nil || len(rows) != 0 || ask.RoutedTo != registry.AskToPerson {
		t.Fatalf("ask=%+v rows=%+v err=%v", ask, rows, err)
	}
}

func TestAClosedPromptWithdrawsItsPendingNotice(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.tell(t, f.d, f.worker, f.waiting)
	f.d.syncWaits(f.worker, nil)
	f.d.background.wait()
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].Status != "canceled" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestAPromptNoticedBeforeARestartIsNotNoticedAgainAfterIt(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.tell(t, f.d, f.worker, f.waiting)
	restarted := newTestDaemon(t, f.reg, f.d.Surfaces)
	open, err := f.reg.OpenPermissionAsks(f.worker.ID)
	if err != nil || len(open) != 1 {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	restarted.tellPermissionAsks(open)
	restarted.background.wait()
	if notices := f.notices(t); len(notices) != 1 {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestACoordinatorThatNeverStartedLeavesThePromptToTheSpawner(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "")
	f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
	if id, err := f.reg.CoordinatorSessionID(registry.CoordinatorSpec{Name: "widget-lead"}); err != nil || id != "" {
		t.Fatalf("the notice started the coordinator: %q %v", id, err)
	}
}

func TestAPromptAgenthailCannotReadStillTellsTheLeadWhatIsWaiting(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.asking.open = nil
	f.tell(t, f.d, f.worker, f.waiting)
	notices := f.notices(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Message, "Needs your approval") {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestAPromptAgenthailCannotAnswerGoesOnlyToThePerson(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	ask := f.tell(t, f.d, f.worker, surface.Waiting{Kind: surface.WaitingQuestion, ID: "unread-1"})
	if notices := f.notices(t); len(notices) != 0 || ask.RoutedTo != registry.AskToPerson || ask.Answerable {
		t.Fatalf("ask=%+v notices=%+v", ask, notices)
	}
}

func TestALeadsCLIAnswerOnItsOwnHostIsRecordedAsTheLeadNotThePerson(t *testing.T) {
	f := newNoticeFixture(t)
	f.setLead(t, "lead")
	f.tell(t, f.d, f.worker, f.waiting)
	open, err := f.reg.OpenPermissionAsks(f.worker.ID)
	if err != nil || len(open) != 1 || open[0].RoutedTo != "lead" {
		t.Fatalf("open=%+v err=%v, want the prompt routed to the lead", open, err)
	}
	handler := f.d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	respond := func(source string) *httptest.ResponseRecorder {
		return postAction(t, handler, map[string]any{"action": "respond", "sessionId": f.worker.ID, "requestId": open[0].ID, "decision": map[string]any{"choices": []string{"allow"}}, "sourceSessionId": source})
	}
	if response := respond("spawner"); response.Code != http.StatusForbidden {
		t.Fatalf("a session the prompt is not routed to answered it from the CLI: %d %s", response.Code, response.Body)
	}
	if response := respond("lead"); response.Code != http.StatusOK {
		t.Fatalf("the lead's CLI answer: %d %s", response.Code, response.Body)
	}
	f.d.background.wait()
	history, err := f.reg.ListHistory(20, f.worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return strings.HasPrefix(entry.Kind, "request-") && strings.Contains(entry.Result, "session lead") && !strings.Contains(entry.Result, "dashboard") && !strings.Contains(entry.Result, "cli")
	}) {
		t.Fatalf("history=%+v, want the answer recorded as the lead's", history)
	}
}
