package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func widgetRepo(t *testing.T, origin string) string {
	t.Helper()
	repo := catalogTestRepo(t)
	gitRun(t, repo, "remote", "add", "origin", origin)
	return repo
}

func moveSession(t *testing.T, node *federationNode, id, cwd string, lastActive time.Time) {
	t.Helper()
	session := node.fake.sessions[id]
	if session.ID == "" {
		session = surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work " + id, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	}
	session.Cwd, session.LastActive = cwd, lastActive
	node.fake.sessions[id] = session
	if err := node.reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
}

func widgetOnTwoHosts(t *testing.T) (*federationNode, *federationNode, registry.PairedHost) {
	t.Helper()
	local, remote, record := pairedFederation(t)
	localRepo := widgetRepo(t, "https://github.com/example/widget.git")
	remoteRepo := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, local, "local-a", localRepo, time.Now())
	moveSession(t, local, "lead", localRepo, time.Now().Add(-72*time.Hour))
	moveSession(t, remote, "remote-a", remoteRepo, time.Now())
	moveSession(t, remote, "remote-b", remoteRepo, time.Now().Add(-48*time.Hour))
	if _, err := local.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: localRepo, Name: "widget-lead", Surface: "codex", Cwd: localRepo}, registry.CoordinatorChange{SessionID: "lead", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: remoteRepo, Name: "vps-lead", Surface: "codex", Cwd: remoteRepo}, registry.CoordinatorChange{SessionID: "remote-a", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	moveSession(t, remote, "remote-c", remoteRepo, time.Now())
	remote.daemon.discoverCatalog(context.Background())
	local.daemon.discoverCatalog(context.Background())
	awaitFederation(t, "the remote conversations and coordinator mirrored in the project", func() bool {
		for _, project := range local.snapshot(t).Projects {
			if project.ID == "repo:github.com/example/widget" {
				return project.SessionCount >= 5 && len(project.Coordinators) == 2
			}
		}
		return false
	})

	return local, remote, record
}

func TestAProjectChannelReachesItsActiveConversationsOnEveryHost(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "sourceSessionId": "local-a"})
	var answer struct {
		Target     broadcast.Target      `json:"target"`
		Recipients []broadcast.Recipient `json:"recipients"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	if answer.Target.ProjectID != "repo:github.com/example/widget" || len(answer.Recipients) != 3 {
		t.Fatalf("answer=%+v", answer)
	}
	byRef := map[string]broadcast.Recipient{}
	for _, recipient := range answer.Recipients {
		ref := recipient.Coordinator
		if ref == "" {
			ref = recipient.SessionID
		}
		byRef[ref] = recipient
	}
	if lead := byRef["@widget-lead"]; lead.Outcome != broadcast.OutcomeSent {
		t.Fatalf("the coordinator outside the active window was not reached: %+v", answer.Recipients)
	}
	if lead := byRef[record.ID+"::@vps-lead"]; lead.Outcome != broadcast.OutcomeQueued {
		t.Fatalf("the paired host's coordinator was not reached once as its coordinator: %+v", answer.Recipients)
	}
	if member := byRef[hosts.Qualify(record.ID, "remote-c")]; member.Outcome != broadcast.OutcomeQueued {
		t.Fatalf("recipients=%+v", answer.Recipients)
	}
	awaitFederation(t, "the remote member received the message", func() bool {
		for _, message := range remote.fake.delivered() {
			if strings.HasSuffix(message, " via #widget] standup") && len(remote.fake.delivered()) == 2 {
				return true
			}
		}
		return false
	})
}

func TestAChannelNameAProjectAnswersToIsRefused(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	if response := runDashboardAction(t, d, `{"action":"channel-create","channel":"widget"}`); response.Code != http.StatusOK {
		t.Fatalf("create before the project: %d %s", response.Code, response.Body)
	}
	repo := widgetRepo(t, "https://github.com/example/widget.git")
	from.Cwd, from.LastActive = repo, time.Now()
	to.Cwd, to.LastActive = repo, time.Now()
	for _, session := range []surface.Session{from, to} {
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())

	collided := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"standup","sourceSessionId":"from"}`)
	if collided.Code != http.StatusBadRequest || !strings.Contains(collided.Body.String(), "#widget is both a channel and project widget (#repo:github.com/example/widget); rename the channel with 'agenthail channel rename widget <new-name>'") {
		t.Fatalf("send to a name a channel and a project share: %d %s", collided.Code, collided.Body)
	}
	if created := runDashboardAction(t, d, `{"action":"channel-create","channel":"widget"}`); created.Code != http.StatusBadRequest || !strings.Contains(created.Body.String(), "#widget already names project widget") {
		t.Fatalf("create a project's name: %d %s", created.Code, created.Body)
	}
	if renamed := runDashboardAction(t, d, `{"action":"channel-rename","channel":"widget","newChannel":"widget-crew"}`); renamed.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body)
	}
	sent := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"standup","sourceSessionId":"from"}`)
	if sent.Code != http.StatusOK || !strings.Contains(sent.Body.String(), `"sessionId":"to"`) || strings.Contains(sent.Body.String(), `"sessionId":"from"`) {
		t.Fatalf("send to the project after the rename: %d %s", sent.Code, sent.Body)
	}
	if len(fake.sent) != 1 || fake.sent[0] != "[from "+d.resolveDisplay("from")+" via #widget] standup" {
		t.Fatalf("sent=%q", fake.sent)
	}
	if archived := runDashboardAction(t, d, `{"action":"session-archive","sessionId":"to"}`); archived.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", archived.Code, archived.Body)
	}
	if empty := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"again","sourceSessionId":"from"}`); empty.Code != http.StatusBadRequest || !strings.Contains(empty.Body.String(), "#widget has no members other than the sender") {
		t.Fatalf("send with only an archived member: %d %s", empty.Code, empty.Body)
	}
}

func TestAProjectNameSeveralProjectsShareIsRefused(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	from.Cwd, from.LastActive = widgetRepo(t, "https://github.com/alpha/widget.git"), time.Now()
	to.Cwd, to.LastActive = widgetRepo(t, "https://github.com/beta/widget.git"), time.Now()
	for _, session := range []surface.Session{from, to} {
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	response := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"standup"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "#widget names several projects: widget (#repo:github.com/alpha/widget), widget (#repo:github.com/beta/widget)") {
		t.Fatalf("send to a shared project name: %d %s", response.Code, response.Body)
	}
	byID := runDashboardAction(t, d, `{"action":"channel-send","channel":"#repo:github.com/beta/widget","message":"standup"}`)
	if byID.Code != http.StatusOK || !strings.Contains(byID.Body.String(), `"sessionId":"to"`) || len(fake.sent) != 1 {
		t.Fatalf("send by project id: %d %s sent=%q", byID.Code, byID.Body, fake.sent)
	}
}

func TestAChannelSendThatFailsForAMemberNamesEveryRecipient(t *testing.T) {
	d, r, fake, _, to := daemonFixture(t)
	if _, err := r.CreateChannel("reviewers"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddToChannel("reviewers", to.ID); err != nil {
		t.Fatal(err)
	}
	fake.sendErr = errors.New("model rejected")
	response := runDashboardAction(t, d, `{"action":"channel-send","channel":"reviewers","message":"handoff"}`)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "0 sent, 0 queued, 0 submitted, 1 failed ("+d.resolveDisplay(to.ID)+" failed: ") || !strings.Contains(response.Body.String(), "model rejected") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestThePersonsChannelPostReachesAnotherHostAsThePersonWithTheirName(t *testing.T) {
	local, remote, _ := widgetOnTwoHosts(t)
	for node, name := range map[*federationNode]string{local: "Ada at her Mac", remote: "root"} {
		if err := node.reg.SetPersonName(name); err != nil {
			t.Fatal(err)
		}
	}
	if response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "author": "person"}); response.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	awaitFederation(t, "the remote member and coordinator received the post", func() bool { return len(remote.fake.delivered()) == 2 })
	remote.fake.mu.Lock()
	sources, persons := append([]string(nil), remote.fake.sources...), append([]string(nil), remote.fake.persons...)
	remote.fake.mu.Unlock()
	for i := range sources {
		if sources[i] != surface.PersonSessionID || persons[i] != "Ada at her Mac" {
			t.Fatalf("sources=%v persons=%v, want the person named by their own host", sources, persons)
		}
	}
	for _, message := range remote.fake.delivered() {
		if !strings.HasPrefix(message, "[from Ada at her Mac via #widget] standup") {
			t.Fatalf("delivered=%q", remote.fake.delivered())
		}
	}
}
