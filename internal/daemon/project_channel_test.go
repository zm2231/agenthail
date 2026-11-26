package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func widgetRepo(t *testing.T, origin string) string {
	t.Helper()
	repo := testkit.GitRepo(t)
	testkit.Git(t, repo, "remote", "add", "origin", origin)
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
	if err := node.reg.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
}

func widgetOnTwoHosts(t *testing.T) (*federationNode, *federationNode, registry.PairedHost) {
	t.Helper()
	return widgetOnTwoHostsWith(t, func(*federation) {})
}

func widgetOnTwoHostsWith(t *testing.T, configure func(*federation)) (*federationNode, *federationNode, registry.PairedHost) {
	t.Helper()
	local, remote, record := pairedFederationWith(t, configure)
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
	testkit.Eventually(t, "the remote conversations and coordinator mirrored in the project", 10*time.Second, func() bool {
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
		Target      broadcast.Target      `json:"target"`
		Recipients  []broadcast.Recipient `json:"recipients"`
		Hosts       []channelHostLeg      `json:"hosts"`
		Unreachable []string              `json:"unreachable"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	if answer.Target.ProjectID != "repo:github.com/example/widget" || len(answer.Recipients) != 3 {
		t.Fatalf("answer=%+v", answer)
	}
	if len(answer.Hosts) != 1 || answer.Hosts[0].ID != record.ID || answer.Hosts[0].State != channelLegReached || answer.Hosts[0].Recipients != 2 || answer.Unreachable == nil || len(answer.Unreachable) != 0 {
		t.Fatalf("the receipt does not join the paired host's leg: hosts=%+v unreachable=%v", answer.Hosts, answer.Unreachable)
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
	if lead := byRef[record.ID+"::@vps-lead"]; lead.Outcome != broadcast.OutcomeSent || lead.HostID != record.ID {
		t.Fatalf("the paired host's coordinator was not reached once as its coordinator: %+v", answer.Recipients)
	}
	if member := byRef[hosts.Qualify(record.ID, "remote-c")]; member.Outcome != broadcast.OutcomeSent || member.HostID != record.ID {
		t.Fatalf("recipients=%+v", answer.Recipients)
	}
	testkit.Eventually(t, "the remote member received the message", 10*time.Second, func() bool {
		for _, message := range remote.fake.delivered() {
			if strings.HasSuffix(message, " via #widget] standup") && len(remote.fake.delivered()) == 2 {
				return true
			}
		}
		return false
	})
}

func TestAProjectChannelSendSkipsTheHostsTheSenderAlreadyReached(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "exceptHosts": []string{record.ID}})
	var answer struct {
		Recipients  []broadcast.Recipient `json:"recipients"`
		Hosts       []channelHostLeg      `json:"hosts"`
		ExceptHosts []string              `json:"exceptHosts"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	if len(answer.Hosts) != 0 {
		t.Fatalf("an excepted host was sent a leg: %+v", answer.Hosts)
	}
	for _, recipient := range answer.Recipients {
		if recipient.HostID == record.ID {
			t.Fatalf("a recipient on the excepted host was reached: %+v", answer.Recipients)
		}
	}
	if len(answer.Recipients) == 0 {
		t.Fatal("the local conversations were not reached")
	}
	if len(answer.ExceptHosts) != 1 || answer.ExceptHosts[0] != record.ID {
		t.Fatalf("the reply does not echo exceptHosts: %v", answer.ExceptHosts)
	}
	if delivered := remote.fake.delivered(); len(delivered) != 0 {
		t.Fatalf("the excepted host delivered %v", delivered)
	}
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
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())

	collided := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"standup","sourceSessionId":"from"}`)
	var collision struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(collided.Body.Bytes(), &collision) != nil || collided.Code != http.StatusBadRequest || collision.Error.Message != "#widget is both a channel and project widget (#repo:github.com/example/widget); rename the channel with 'agenthail channel rename widget <new-name>', or address the project by its id #repo:github.com/example/widget" {
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
	if empty := runDashboardAction(t, d, `{"action":"channel-send","channel":"widget","message":"again","sourceSessionId":"from"}`); empty.Code != http.StatusBadRequest || !strings.Contains(empty.Body.String(), "#widget has no reachable members other than the sender; skipped "+d.resolveDisplay("to")+" (archived)") {
		t.Fatalf("send with only an archived member: %d %s", empty.Code, empty.Body)
	}
}

func TestAProjectNameSeveralProjectsShareIsRefused(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	from.Cwd, from.LastActive = widgetRepo(t, "https://github.com/alpha/widget.git"), time.Now()
	to.Cwd, to.LastActive = widgetRepo(t, "https://github.com/beta/widget.git"), time.Now()
	for _, session := range []surface.Session{from, to} {
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
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
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Target     broadcast.Target      `json:"target"`
		Recipients []broadcast.Recipient `json:"recipients"`
		Sent       *int                  `json:"sent"`
		Queued     *int                  `json:"queued"`
		Submitted  *int                  `json:"submitted"`
		Failed     *int                  `json:"failed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "conflict" || body.Target.Channel != "reviewers" || body.Sent == nil || *body.Sent != 0 || body.Queued == nil || body.Submitted == nil || body.Failed == nil || *body.Failed != 1 {
		t.Fatalf("the partial failure does not carry the per-member fields: %s", response.Body)
	}
	if len(body.Recipients) != 1 || body.Recipients[0].SessionID != to.ID || body.Recipients[0].Outcome != broadcast.OutcomeFailed || !strings.Contains(body.Recipients[0].Detail, "model rejected") {
		t.Fatalf("recipients=%+v", body.Recipients)
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
	testkit.Eventually(t, "the remote member and coordinator received the post", 10*time.Second, func() bool { return len(remote.fake.delivered()) == 2 })
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

func TestAProjectPostReachesLiveConversationsAndSkipsOnesItCannotReach(t *testing.T) {
	local, _, _ := widgetOnTwoHosts(t)
	repo := local.fake.sessions["local-a"].Cwd
	removed := widgetRepo(t, "https://github.com/example/widget.git")
	for _, id := range []string{"live", "ended", "ended-pinned", "ended-old", "archived", "folder-gone", "unloaded"} {
		cwd, lastActive := repo, time.Now().Add(-time.Hour)
		switch id {
		case "ended-pinned", "ended-old":
			lastActive = time.Now().Add(-72 * time.Hour)
		case "folder-gone":
			cwd = removed
		}
		moveSession(t, local, id, cwd, lastActive)
	}
	unloaded := local.fake.sessions["unloaded"]
	unloaded.Status, unloaded.Ended = surface.StatusNotLoaded, true
	local.fake.sessions["unloaded"] = unloaded
	if err := local.reg.RecordObservedSession(unloaded); err != nil {
		t.Fatal(err)
	}
	if _, err := local.reg.PinSession("ended-pinned", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := local.reg.ArchiveSession("archived", time.Now()); err != nil {
		t.Fatal(err)
	}
	local.daemon.discoverCatalog(context.Background())
	for _, id := range []string{"ended", "ended-pinned", "ended-old"} {
		delete(local.fake.sessions, id)
	}
	ended := func() bool {
		snapshot, err := local.reg.CatalogSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range snapshot.Sessions {
			if record.Session.ID == "ended" {
				return record.Session.Status == surface.StatusOffline
			}
		}
		return false
	}
	for range 3 {
		if ended() {
			break
		}
		local.daemon.discoverCatalog(context.Background())
	}
	if !ended() {
		t.Fatal("a conversation discovery no longer lists did not read as ended")
	}
	if err := os.RemoveAll(removed); err != nil {
		t.Fatal(err)
	}

	post := func() (*httptest.ResponseRecorder, map[string]broadcast.Recipient, int) {
		t.Helper()
		before := len(local.fake.delivered())
		response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#widget", "message": "release notes", "sourceSessionId": "local-a"})
		var answer struct {
			Recipients []broadcast.Recipient `json:"recipients"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
			t.Fatalf("send status=%d body=%s", response.Code, response.Body)
		}
		members := map[string]broadcast.Recipient{}
		for _, recipient := range answer.Recipients {
			if recipient.HostID == "" && recipient.Coordinator == "" {
				members[recipient.SessionID] = recipient
			}
		}
		return response, members, len(local.fake.delivered()) - before
	}
	response, members, delivered := post()
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"skipped":5`) {
		t.Fatalf("a post every reachable member got: status=%d body=%s", response.Code, response.Body)
	}
	want := map[string]broadcast.Recipient{
		"live":         {SessionID: "live", Outcome: broadcast.OutcomeSent, Detail: members["live"].Detail},
		"ended":        {SessionID: "ended", Outcome: broadcast.OutcomeSkipped, Detail: "ended"},
		"ended-pinned": {SessionID: "ended-pinned", Outcome: broadcast.OutcomeSkipped, Detail: "ended"},
		"archived":     {SessionID: "archived", Outcome: broadcast.OutcomeSkipped, Detail: "archived"},
		"unloaded":     {SessionID: "unloaded", Outcome: broadcast.OutcomeSkipped, Detail: "ended"},
		"folder-gone":  {SessionID: "folder-gone", Outcome: broadcast.OutcomeSkipped, Detail: "its folder " + removed + " is gone"},
	}
	if len(members) != len(want) {
		t.Fatalf("this host's members %+v, want %+v", members, want)
	}
	for id, recipient := range want {
		if members[id] != recipient {
			t.Fatalf("member %s = %+v, want %+v", id, members[id], recipient)
		}
	}
	if delivered != 2 {
		t.Fatalf("this host delivered %d messages, want its coordinator and the live member only", delivered)
	}
	if err := local.reg.SyncDeliveryAsks(time.Now()); err != nil {
		t.Fatal(err)
	}
	if asks, err := local.reg.OpenAsks(); err != nil || slices.ContainsFunc(asks, func(ask registry.Ask) bool { return ask.Origin == "delivery" }) {
		t.Fatalf("a post that skipped members raised a delivery ask: %+v %v", asks, err)
	}

	for channel, members := range map[string][]string{"crew": {"live", "ended", "archived", "folder-gone", "unloaded", "lead"}, "retired": {"ended", "archived"}} {
		if _, err := local.reg.CreateChannel(channel); err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			if err := local.reg.AddToChannel(channel, member); err != nil {
				t.Fatal(err)
			}
		}
	}
	lead := local.fake.sessions["lead"]
	lead.Status, lead.Ended = surface.StatusNotLoaded, true
	local.fake.sessions["lead"] = lead
	if err := local.reg.RecordObservedSession(lead); err != nil {
		t.Fatal(err)
	}
	before := len(local.fake.delivered())
	crew := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#crew", "message": "handoff", "sourceSessionId": "local-a"})
	var crewAnswer struct {
		Recipients []broadcast.Recipient `json:"recipients"`
	}
	if crew.Code != http.StatusOK || json.Unmarshal(crew.Body.Bytes(), &crewAnswer) != nil {
		t.Fatalf("a channel post every reachable member got: status=%d body=%s", crew.Code, crew.Body)
	}
	got := map[string]broadcast.Recipient{}
	for _, recipient := range crewAnswer.Recipients {
		got[recipient.SessionID] = recipient
	}
	for id, recipient := range want {
		if id == "ended-pinned" {
			continue
		}
		if delivered := got[id].Outcome; recipient.Outcome == broadcast.OutcomeSent && delivered != broadcast.OutcomeFailed && delivered != broadcast.OutcomeSkipped {
			recipient = got[id]
		}
		if got[id] != recipient {
			t.Fatalf("channel member %s = %+v, want %+v", id, got[id], recipient)
		}
	}
	if outcome := got["lead"].Outcome; outcome == broadcast.OutcomeSkipped || outcome == broadcast.OutcomeFailed {
		t.Fatalf("an ended coordinator in the channel was not reached: %+v", got["lead"])
	}
	if len(got) != 6 || len(local.fake.delivered())-before != 2 {
		t.Fatalf("the channel post reached %+v and delivered %d, want the live member and the coordinator delivered", crewAnswer.Recipients, len(local.fake.delivered())-before)
	}
	retired := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#retired", "message": "handoff"})
	if retired.Code != http.StatusBadRequest || !strings.Contains(retired.Body.String(), "#retired has no reachable members other than the sender; skipped ") {
		t.Fatalf("a channel post every member of which was skipped: status=%d body=%s", retired.Code, retired.Body)
	}
	if err := local.reg.SyncDeliveryAsks(time.Now()); err != nil {
		t.Fatal(err)
	}
	if asks, err := local.reg.OpenAsks(); err != nil || slices.ContainsFunc(asks, func(ask registry.Ask) bool { return ask.Origin == "delivery" }) {
		t.Fatalf("a channel post that skipped members raised a delivery ask: %+v %v", asks, err)
	}

	local.fake.failSends(surface.DeliveryTerminal(errors.New("model rejected")))
	response, members, _ = post()
	if response.Code != http.StatusConflict || members["live"].Outcome != broadcast.OutcomeFailed || members["ended"].Outcome != broadcast.OutcomeSkipped {
		t.Fatalf("a failure to a reachable member: status=%d members=%+v", response.Code, members)
	}
	if message := response.Body.String(); !strings.Contains(message, local.daemon.resolveDisplay("live")+" failed: ") || strings.Contains(message, local.daemon.resolveDisplay("ended")+" failed") {
		t.Fatalf("the failure names the wrong members: %s", message)
	}
}

func TestAProjectPostWhoseOnlyMembersAnotherHostSkippedIsRefused(t *testing.T) {
	local, remote, _ := pairedFederationWith(t, func(*federation) {})
	repo := widgetRepo(t, "https://github.com/example/gadget.git")
	moveSession(t, remote, "gadget-worker", repo, time.Now())
	remote.daemon.discoverCatalog(context.Background())
	local.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the paired host's conversation mirrored in the project", 10*time.Second, func() bool {
		for _, project := range local.snapshot(t).Projects {
			if project.ID == "repo:github.com/example/gadget" {
				return project.SessionCount >= 1
			}
		}
		return false
	})
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#gadget", "message": "release notes"})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "#gadget has no reachable members other than the sender; skipped ") || !strings.Contains(response.Body.String(), repo+" is gone") {
		t.Fatalf("a post every member of which was skipped: status=%d body=%s", response.Code, response.Body)
	}
	if delivered := remote.fake.delivered(); len(delivered) != 0 {
		t.Fatalf("the paired host delivered %q", delivered)
	}
}

func TestAProjectChannelSkipsOneOffRunsUntilATaskSettles(t *testing.T) {
	local, _, _ := widgetOnTwoHosts(t)
	repo := local.fake.sessions["local-a"].Cwd
	for _, id := range []string{"worker", "task-running", "task-settled", "exec-run", "spawned-agent"} {
		moveSession(t, local, id, repo, time.Now())
	}
	exec := local.fake.sessions["exec-run"]
	exec.Source = "exec"
	local.fake.sessions["exec-run"] = exec
	spawned := local.fake.sessions["spawned-agent"]
	spawned.Subagent = &surface.Subagent{ParentID: "worker"}
	local.fake.sessions["spawned-agent"] = spawned
	for _, id := range []string{"exec-run", "spawned-agent"} {
		if err := local.reg.RecordObservedSession(local.fake.sessions[id]); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range []string{"task-running", "task-settled"} {
		if _, err := local.reg.CreateTask("local-a", child, "check the index", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := local.reg.SettleTask("task-settled", registry.TaskSettlement{State: registry.TaskDone, Kind: registry.TaskKindReported, Result: "done"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	local.daemon.discoverCatalog(context.Background())
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "channel": "#widget", "message": "release notes", "sourceSessionId": "local-a"})
	var answer struct {
		Recipients []broadcast.Recipient `json:"recipients"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body)
	}
	reached := map[string]bool{}
	for _, recipient := range answer.Recipients {
		if recipient.HostID == "" && recipient.Coordinator == "" {
			reached[recipient.SessionID] = true
		}
	}
	if !reached["worker"] || !reached["task-settled"] || reached["task-running"] || reached["exec-run"] || reached["spawned-agent"] {
		t.Fatalf("this host's members %v, want the worker and the settled task's session but no running task, exec run or sub-agent: %+v", reached, answer.Recipients)
	}
}
