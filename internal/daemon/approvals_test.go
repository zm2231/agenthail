package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type approvalFixture struct {
	d      *Daemon
	r      *registry.Registry
	worker surface.Session
	lead   surface.Session
	pushes *pushRecorder
}

func newApprovalFixture(t *testing.T) approvalFixture {
	t.Helper()
	d, r, fake, worker, lead := daemonFixture(t)
	pushes := newPushRecorder(t, r)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	worker.Cwd, lead.Cwd = widget, widget
	for _, session := range []surface.Session{worker, lead} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	project, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindClaude), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	return approvalFixture{d: d, r: r, worker: worker, lead: lead, pushes: pushes}
}

func (f approvalFixture) askPerson(t *testing.T, body string) string {
	t.Helper()
	answer := askScope(t, f.d, daemonapi.ScopeRequest{SessionID: f.worker.ID, Action: "operator", Payload: scopePayload(daemonapi.PersonActionAPI, body), AskPerson: true, Reason: "the release needs it"}, "127.0.0.1:50000")
	if answer.status != http.StatusOK || answer.reply.AskID == "" {
		t.Fatalf("asking the person answered %d %s", answer.status, answer.body)
	}
	return answer.reply.AskID
}

func (f approvalFixture) respond(t *testing.T, p principal, askID, choice string) actionReply {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"action": "respond", "sessionId": f.worker.ID, "requestId": askID, "decision": map[string]any{"choices": []string{choice}}})
	return f.d.performAction(context.Background(), p, actionFormatAPIv1, body, "")
}

var thePerson = principal{ID: "phone", Kind: principalDevice, HostName: "phone"}

func TestAnAgentAsksThePersonForARefusedActionAndItRunsOnceWhenAllowed(t *testing.T) {
	f := newApprovalFixture(t)
	refused := askScope(t, f.d, daemonapi.ScopeRequest{SessionID: f.worker.ID, Action: "operator", Payload: scopePayload(daemonapi.PersonActionAPI, `{"action":"channel-create","channel":"release"}`)}, "127.0.0.1:50000")
	if refused.status != http.StatusForbidden || refused.rule != "person_only" || !strings.Contains(string(refused.body), "--ask-person") {
		t.Fatalf("the refusal is %d %s, want one that says how to ask the person", refused.status, refused.body)
	}

	askID := f.askPerson(t, `{"action":"channel-create","channel":"release"}`)
	if again := f.askPerson(t, `{"action":"channel-create","channel":"release"}`); again != askID {
		t.Fatalf("asking again raised %s, want the open %s", again, askID)
	}

	rows, err := f.r.RowAsks()
	if err != nil {
		t.Fatal(err)
	}
	if row := rows[f.worker.ID]; row.ID != askID || !slices.Equal(row.Open, []string{askID}) {
		t.Fatalf("the asking row shows %s open %v, want the approval %s", row.ID, row.Open, askID)
	}
	open, err := f.d.openRequests(context.Background(), &f.worker)
	requests := open.requests
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].ID != askID || !strings.Contains(requests[0].Questions[0].Question, "Create channel #release") || !strings.Contains(requests[0].Questions[0].Question, "the release needs it") {
		t.Fatalf("the person's requests are %+v, want the approval naming the action and the reason", requests)
	}
	testkit.Eventually(t, "the approval on the person's phone", 5*time.Second, func() bool { return f.pushes.count("ask.opened") == 1 })

	asAgent := principal{ID: f.lead.ID, Kind: principalSession, Claimed: f.lead.ID}
	actingForThePerson := principal{ID: f.lead.ID, Kind: principalSession, Claimed: f.lead.ID, ForPerson: true}
	for _, session := range []principal{asAgent, actingForThePerson} {
		if reply := f.respond(t, session, askID, registry.ApprovalAllow); reply.Status != http.StatusForbidden {
			t.Fatalf("a session (for the person: %v) allowing it answered %d %s, want 403", session.ForPerson, reply.Status, reply.Body)
		}
	}
	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusOK {
		t.Fatalf("the person allowing it answered %d %s", reply.Status, reply.Body)
	}
	if exists, err := f.r.ChannelExists("release"); err != nil || !exists {
		t.Fatalf("the allowed action did not run: %v %v", exists, err)
	}
	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusConflict {
		t.Fatalf("a second answer answered %d %s, want it stale", reply.Status, reply.Body)
	}
	if got := noticesTo(t, f.r, f.worker.ID, "The person allowed: Create channel #release (ask "+askID+"). It ran"); got != 1 {
		t.Fatalf("the agent was told %d times, want once", got)
	}
	stored, _, err := f.r.Approval(askID)
	if err != nil || stored.Status != registry.ApprovalRan || stored.DecidedBy != thePerson.ID {
		t.Fatalf("the record is %+v (%v)", stored, err)
	}
}

func TestADeniedApprovalRunsNothingAndTellsTheAgent(t *testing.T) {
	f := newApprovalFixture(t)
	askID := f.askPerson(t, `{"action":"channel-create","channel":"denied"}`)
	if reply := f.respond(t, thePerson, askID, registry.ApprovalDeny); reply.Status != http.StatusOK {
		t.Fatalf("deny answered %d %s", reply.Status, reply.Body)
	}
	if exists, _ := f.r.ChannelExists("denied"); exists {
		t.Fatal("a denied action ran")
	}
	if got := noticesTo(t, f.r, f.worker.ID, "The person denied: Create channel #denied (ask "+askID+")."); got != 1 {
		t.Fatalf("the agent was told %d times, want once", got)
	}
	again := f.askPerson(t, `{"action":"channel-create","channel":"denied"}`)
	if reply := f.respond(t, thePerson, again, registry.ApprovalDeny); again == askID || reply.Status != http.StatusOK {
		t.Fatalf("the second ask %s answered %d %s", again, reply.Status, reply.Body)
	}
	if got := noticesTo(t, f.r, f.worker.ID, "The person denied: Create channel #denied (ask "+again+")."); got != 1 {
		t.Fatalf("the second denial reached the agent %d times, want once and apart from the first", got)
	}
}

func TestAnApprovalToSetACoordinatorNamesTheNameItGivesAndTheNameItReplaces(t *testing.T) {
	f := newApprovalFixture(t)
	question := func(body string) string {
		ask, found, err := f.r.Ask(f.askPerson(t, body))
		if err != nil || !found || len(ask.Questions) != 1 {
			t.Fatalf("the ask for %s is %+v (%v)", body, ask, err)
		}
		return ask.Questions[0].Question
	}
	if got := question(`{"action":"coordinator-set","sessionId":"` + f.lead.ID + `","name":"vera","replace":true}`); !strings.HasPrefix(got, "Make its project's coordinator as @vera, renaming it from @widget-lead, the session ") {
		t.Fatalf("renaming the lead asks %q", got)
	}
	if got := question(`{"action":"coordinator-set","sessionId":"` + f.worker.ID + `","replace":true}`); !strings.HasPrefix(got, "Make its project's coordinator as @widget-lead the session ") || strings.Contains(got, "renaming") {
		t.Fatalf("a set without a name asks %q, want the project's default name", got)
	}
	long := surface.Session{ID: "long-named", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Name: strings.Repeat("a long session title ", 12), Cwd: f.worker.Cwd, LastActive: time.Now()}
	if err := f.r.RecordObservedSession(long); err != nil {
		t.Fatal(err)
	}
	if got := question(`{"action":"coordinator-set","sessionId":"long-named","name":"scribe","replace":true}`); got != "Make its project's coordinator as @scribe the session long-named. Why: the release needs it" {
		t.Fatalf("a long-named session's set asks %q, want the name before the cut", got)
	}
	longName := strings.Repeat("n", 80)
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "elsewhere", ProjectRoot: "/elsewhere", Name: longName, Surface: string(surface.KindClaude), Cwd: "/elsewhere"}, registry.CoordinatorChange{SessionID: "long-named", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	renameID := f.askPerson(t, `{"action":"coordinator-set","sessionId":"long-named","name":"`+strings.Repeat("m", 80)+`","replace":true}`)
	response := httptest.NewRecorder()
	f.d.requestsHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId="+f.worker.ID, nil))
	var open struct {
		Requests []struct {
			ID        string                  `json:"id"`
			Questions []surface.Question      `json:"questions"`
			Actions   []surface.RequestAction `json:"actions"`
		} `json:"requests"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &open) != nil {
		t.Fatalf("requests answered %d %s", response.Code, response.Body)
	}
	var rename surface.Question
	renameButtons, shortWithButtons := -1, 0
	for _, request := range open.Requests {
		switch {
		case request.ID == renameID && len(request.Questions) == 1:
			rename, renameButtons = request.Questions[0], len(request.Actions)
		case len(request.Actions) > 0:
			shortWithButtons++
		}
	}
	if renameButtons != 0 || shortWithButtons == 0 {
		t.Fatalf("the clipped rename offers %d notification buttons and %d short approvals offer any, want none for the rename so it is answered only once opened", renameButtons, shortWithButtons)
	}
	want := "Make its project's coordinator as @" + strings.Repeat("m", 80) + ", renaming it from @" + longName + ", the session long-named. Why: the release needs it"
	if !strings.HasPrefix(rename.Question, "Make its project's coordinator as @"+strings.Repeat("m", 80)) || len([]rune(rename.Question)) > registry.MaxAskQuestion || rename.Detail != want {
		t.Fatalf("renaming between two 80-character names shows %q with detail %q, want the new name in the label and both names whole in the detail", rename.Question, rename.Detail)
	}
	if got := question(`{"action":"coordinator-new","surface":"claude","cwd":"/work/widget"}`); !strings.HasPrefix(got, "Start a new claude coordinator with its project's default name for /work/widget. Why: ") {
		t.Fatalf("a new coordinator without a name asks %q", got)
	}
	if got := question(`{"action":"coordinator-new","surface":"claude","cwd":"/work/widget","name":"@scout"}`); !strings.HasPrefix(got, "Start a new claude coordinator as @scout for /work/widget. Why: ") {
		t.Fatalf("a named new coordinator asks %q", got)
	}
}

func TestAnUnansweredApprovalExpiresAfterADayAndTellsTheAgent(t *testing.T) {
	f := newApprovalFixture(t)
	askID := f.askPerson(t, `{"action":"channel-create","channel":"late"}`)
	f.d.sweepAsks(time.Now().Add(23 * time.Hour))
	if ask, _, _ := f.r.Ask(askID); ask.State != registry.AskOpen {
		t.Fatalf("before a day the approval is %s", ask.State)
	}
	f.d.sweepAsks(time.Now().Add(approvalLifetime + time.Minute))
	if ask, _, _ := f.r.Ask(askID); ask.State != registry.AskExpired {
		t.Fatalf("after a day the approval is %s, want expired", ask.State)
	}
	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusConflict {
		t.Fatalf("allowing an expired approval answered %d %s", reply.Status, reply.Body)
	}
	if got := noticesTo(t, f.r, f.worker.ID, "did not answer within a day"); got != 1 {
		t.Fatalf("the agent was told %d times, want once", got)
	}
}

func TestACoordinatorRunsItsOwnProjectsChangesWithoutAsking(t *testing.T) {
	f := newApprovalFixture(t)
	project, err := broadcast.FindProject(f.r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	cases := []struct {
		name   string
		caller string
		body   string
		status int
	}{
		{"the lead renames its project", f.lead.ID, `{"action":"project-rename","projectId":"` + project.ID + `","title":"Widgets"}`, http.StatusOK},
		{"a worker renames its project", f.worker.ID, `{"action":"project-rename","projectId":"` + project.ID + `","title":"Widgets"}`, http.StatusForbidden},
		{"the lead renames another project", f.lead.ID, `{"action":"project-rename","projectId":"implicit:` + outside + `","title":"Mine"}`, http.StatusForbidden},
		{"the lead links a folder into its project", f.lead.ID, `{"action":"project-link","projectId":"implicit:` + outside + `","survivor":"` + project.ID + `"}`, http.StatusOK},
		{"the lead links its project into another", f.lead.ID, `{"action":"project-link","projectId":"` + project.ID + `","survivor":"implicit:` + outside + `"}`, http.StatusForbidden},
		{"the lead adds a folder in no project", f.lead.ID, `{"action":"project-add","path":"` + outside + `"}`, http.StatusOK},
		{"the lead adds a folder on another host", f.lead.ID, `{"action":"project-add","path":"` + outside + `","host":"elsewhere"}`, http.StatusForbidden},
		{"the lead adds a folder already in a project", f.lead.ID, `{"action":"project-add","path":"` + f.lead.Cwd + `"}`, http.StatusForbidden},
		{"the lead archives its project", f.lead.ID, `{"action":"project-archive","projectId":"` + project.ID + `"}`, http.StatusOK},
		{"the lead removes its project", f.lead.ID, `{"action":"project-remove","projectId":"` + project.ID + `"}`, http.StatusOK},
		{"the lead turns its project's tools off", f.lead.ID, `{"action":"project-setting","projectId":"` + project.ID + `","setting":"mcp","value":"off"}`, http.StatusOK},
		{"the lead clears its project's coordinator", f.lead.ID, `{"action":"coordinator-clear","projectId":"` + project.ID + `"}`, http.StatusOK},
	}
	for _, c := range cases {
		answer := askScope(t, f.d, daemonapi.ScopeRequest{SessionID: c.caller, Action: "operator", Payload: scopePayload(daemonapi.PersonActionAPI, c.body)}, "127.0.0.1:50000")
		if answer.status != c.status {
			t.Errorf("%s: %d %s, want %d", c.name, answer.status, answer.body, c.status)
		}
	}
}

type unreadableRequests struct{ *daemonSurface }

func (unreadableRequests) Requests(context.Context, *surface.Session) (surface.RequestState, error) {
	return surface.RequestState{}, errors.New("Codex's app-server is not running")
}

func (unreadableRequests) Respond(context.Context, *surface.Session, string, surface.Decision) (string, error) {
	return "", errors.New("Codex's app-server is not running")
}

func TestAnApprovalShowsWhileItsSessionsProviderCannotBeRead(t *testing.T) {
	f := newApprovalFixture(t)
	f.d.Surfaces = []surface.Surface{unreadableRequests{f.d.Surfaces[0].(*daemonSurface)}}
	askID := f.askPerson(t, `{"action":"channel-create","channel":"offline"}`)
	response := httptest.NewRecorder()
	f.d.requestsHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId="+f.worker.ID, nil))
	var body struct {
		ProviderError string `json:"providerError"`
		Requests      []struct {
			ID string `json:"id"`
		} `json:"requests"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Requests) != 1 || body.Requests[0].ID != askID || !strings.Contains(body.ProviderError, "not running") {
		t.Fatalf("requests answered %d %s, want the approval %s and the provider's error", response.Code, response.Body, askID)
	}
	caller, err := f.d.scopeCaller(f.worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	one, err := f.d.mcpSessionRequests(context.Background(), caller, f.worker.ID, askID)
	if reply, _ := one.(map[string]any); err != nil || reply["request"] == nil || !strings.Contains(fmt.Sprint(reply["providerError"]), "not running") {
		t.Fatalf("agenthail_requests for the approval answered %+v (%v), want it and the provider's error", one, err)
	}
	if reply := f.respond(t, thePerson, askID, registry.ApprovalAllow); reply.Status != http.StatusOK {
		t.Fatalf("allowing it answered %d %s", reply.Status, reply.Body)
	}
	if exists, err := f.r.ChannelExists("offline"); err != nil || !exists {
		t.Fatalf("the allowed action did not run: %v %v", exists, err)
	}
}

func TestACoordinatorClearsOnlyTheQueuedMessagesItsProjectSent(t *testing.T) {
	f := newApprovalFixture(t)
	clear := func(senders string) int {
		return askScope(t, f.d, daemonapi.ScopeRequest{SessionID: f.lead.ID, Action: "operator", Payload: scopePayload(daemonapi.PersonActionAPI, `{"action":"queue-clear","sessionId":"`+f.worker.ID+`","senders":`+senders+`}`)}, "127.0.0.1:50000").status
	}
	outsider := surface.Session{ID: "outsider", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: t.TempDir(), LastActive: time.Now()}
	if err := f.r.RecordObservedSession(outsider); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		senders string
		status  int
	}{{`["` + f.lead.ID + `",""]`, http.StatusOK}, {`["` + f.lead.ID + `","outsider"]`, http.StatusForbidden}, {`null`, http.StatusForbidden}} {
		if status := clear(c.senders); status != c.status {
			t.Errorf("clearing the messages from %s answered %d, want %d", c.senders, status, c.status)
		}
	}
	if _, err := f.r.QueueMessageWithOptions(f.worker.ID, "from the lead", "", surface.SendOptions{SourceSessionID: f.lead.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.QueueMessageWithOptions(f.worker.ID, "from elsewhere", "", surface.SendOptions{SourceSessionID: outsider.ID}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": "queue-clear", "sessionId": f.worker.ID, "senders": []string{f.lead.ID}})
	if reply := f.d.performAction(context.Background(), thePerson, actionFormatAPIv1, body, ""); reply.Status != http.StatusOK {
		t.Fatalf("queue-clear answered %d %s", reply.Status, reply.Body)
	}
	if senders, err := f.r.PendingSenders(f.worker.ID); err != nil || !slices.Equal(senders, []string{outsider.ID}) {
		t.Fatalf("after clearing the lead's messages the pending senders are %v (%v), want only the outsider's", senders, err)
	}
}

func TestAnApprovalIsTitledWithTheNameTheAskingRowShows(t *testing.T) {
	f := newApprovalFixture(t)
	if err := f.r.SetAlias("builder", f.worker.ID); err != nil {
		t.Fatal(err)
	}
	askID := f.askPerson(t, `{"action":"channel-create","channel":"named"}`)
	ask, _, err := f.r.Ask(askID)
	if err != nil || ask.Title != "Create channel #named" {
		t.Fatalf("the approval is titled %q (%v), want the row's @builder", ask.Title, err)
	}
}
