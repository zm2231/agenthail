package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const widgetProject = "repo:github.com/example/widget"

type hostTargets struct {
	local, remote *federationNode
	record        registry.PairedHost
	caller        *mcp.ClientSession
}

// newHostTargets pairs two daemons with the widget project on both: here
// local-a; on the paired host vps remote-a, remote-c (@builder) and, in the
// gadget project, remote-b. The project's coordinator is local-a when
// localLead is set, else remote-a. The MCP caller is local-a.
func newHostTargets(t *testing.T, localLead bool) hostTargets {
	t.Helper()
	local, remote, record := pairedFederation(t)
	widgetHere := widgetRepo(t, "https://github.com/example/widget.git")
	widgetThere := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, local, "local-a", widgetHere, time.Now())
	moveSession(t, remote, "remote-a", widgetThere, time.Now())
	moveSession(t, remote, "remote-b", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	moveSession(t, remote, "remote-c", widgetThere, time.Now())
	if err := remote.reg.SetAlias("builder", "remote-c"); err != nil {
		t.Fatal(err)
	}
	if localLead {
		setLead(t, local, widgetProject, widgetHere, "widget-lead", "local-a", registry.CoordinatorChange{})
	} else {
		setLead(t, remote, widgetProject, widgetThere, "widget-lead", "remote-a", registry.CoordinatorChange{})
	}
	remote.daemon.discoverCatalog(context.Background())
	local.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the paired host's sessions, alias and coordinator mirrored", 10*time.Second, func() bool {
		matches, _ := local.reg.MatchHostSessions(record.ID, "@builder")
		project, _ := local.reg.CatalogSessionProject("remote-c")
		if len(matches) != 1 || hostProjectID(project) != widgetProject {
			return false
		}
		if localLead {
			return true
		}
		leads, _ := local.reg.ProjectCoordinators(widgetProject)
		return len(leads) == 1 && leads[0].HostID == record.ID && leads[0].SessionID != ""
	})
	caller, err := local.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, local.daemon), issueMCPToken(t, local.daemon, *caller))
	return hostTargets{local: local, remote: remote, record: record, caller: session}
}

func (h hostTargets) send(t *testing.T, target, message string, extra map[string]any) (map[string]any, bool) {
	t.Helper()
	args := map[string]any{"target": target, "message": message, "clientRequestId": "send-" + message}
	for key, value := range extra {
		args[key] = value
	}
	return callMCP(t, h.caller, "agenthail_send", args)
}

func (h hostTargets) reached(message string) bool {
	return slices.ContainsFunc(h.remote.fake.delivered(), func(got string) bool { return strings.Contains(got, message) })
}

func refusedFor(t *testing.T, out map[string]any, refused bool, code, rule string) {
	t.Helper()
	detail, _ := out["detail"].(map[string]any)
	if !refused || out["code"] != code || (rule != "" && detail["rule"] != rule) {
		t.Fatalf("refused=%v out=%v, want %s/%s", refused, out, code, rule)
	}
}

func TestAWorkersQueuedSendReachesItsLeadOnAPairedHost(t *testing.T) {
	h := newHostTargets(t, false)
	if out, refused := h.send(t, "@lead", "status: tests pass", nil); refused || !h.reached("status: tests pass") {
		t.Fatalf("@lead across hosts: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	if out, refused := h.send(t, "vps::remote-a", "by host and id", nil); refused || !h.reached("by host and id") {
		t.Fatalf("host::id: refused=%v out=%v", refused, out)
	}
	if !slices.Contains(h.remote.fake.senders(), surface.UnrepliableSender(mustHostID(t, h.local), "local-a", "").ID) {
		t.Fatalf("senders=%q, want the worker named by its host", h.remote.fake.senders())
	}
}

func TestAWorkersQueuedSendReachesAPeerInItsProjectOnAPairedHost(t *testing.T) {
	h := newHostTargets(t, false)
	if out, refused := h.send(t, "vps::@builder", "to a peer", nil); refused || !h.reached("to a peer") {
		t.Fatalf("a queued send to a peer: refused=%v out=%v delivered=%q", refused, out, h.remote.fake.delivered())
	}
	out, refused := h.send(t, "vps::@builder", "to a peer now", map[string]any{"mode": "now"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	if err := h.remote.reg.ArchiveSession("remote-c", time.Now()); err != nil {
		t.Fatal(err)
	}
	out, refused = h.send(t, "vps::@builder", "to an archived peer", nil)
	refusedFor(t, out, refused, "out_of_scope", "archived")
	for _, message := range []string{"to a peer now", "to an archived peer"} {
		if h.reached(message) {
			t.Fatalf("a refused send reached the paired host: %q", message)
		}
	}
}

func TestACrossHostSendRefusalNamesTheRuleItFailed(t *testing.T) {
	h := newHostTargets(t, false)
	out, refused := h.send(t, "vps::remote-b", "to another project", nil)
	refusedFor(t, out, refused, "out_of_scope", "different_project")
	out, refused = h.send(t, "@lead", "right now", map[string]any{"mode": "now"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	out, refused = callMCP(t, h.caller, "agenthail_steer", map[string]any{"target": "vps::remote-a", "message": "turn left", "clientRequestId": "steer-1"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	out, refused = h.send(t, "nowhere::@builder", "to no host", nil)
	refusedFor(t, out, refused, "not_found", "")
	for _, message := range []string{"to another project", "right now", "turn left"} {
		if h.reached(message) {
			t.Fatalf("a refused send reached the paired host: %q", message)
		}
	}
}

func TestACoordinatorReachesItsProjectsSessionsOnAPairedHost(t *testing.T) {
	h := newHostTargets(t, true)
	for target, message := range map[string]string{"vps::@builder": "by host and alias", "@builder": "by alias alone", "vps::remote-c": "by host and id"} {
		if out, refused := h.send(t, target, message, nil); refused || !h.reached(message) {
			t.Fatalf("%s: refused=%v out=%v", target, refused, out)
		}
	}
	out, refused := h.send(t, "vps::remote-b", "outside the project", nil)
	refusedFor(t, out, refused, "out_of_scope", "different_project")
	out, refused = h.send(t, "vps::work remote-c", "by a name fragment", nil)
	refusedFor(t, out, refused, "invalid", "")
	if h.reached("by a name fragment") {
		t.Fatal("a name fragment on the paired host was delivered to")
	}
}

func TestACrossHostSendIsJudgedByTheProjectThePairedHostReportsNow(t *testing.T) {
	h := newHostTargets(t, true)
	moveSession(t, h.remote, "remote-c", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	out, refused := h.send(t, "vps::@builder", "after it moved", nil)
	refusedFor(t, out, refused, "out_of_scope", "different_project")
	if h.reached("after it moved") {
		t.Fatal("a refused send reached the paired host")
	}
}

func (h hostTargets) goAway(t *testing.T) {
	t.Helper()
	h.remote.gate.proxyDown.Store(true)
	h.remote.server.CloseClientConnections()
	testkit.Eventually(t, "the paired host shown down", 10*time.Second, func() bool { return !h.local.daemon.hostConnected(h.record.ID) })
}

func (h hostTargets) comeBack(t *testing.T) {
	t.Helper()
	h.remote.gate.proxyDown.Store(false)
}

func queuedReceipt(t *testing.T, out map[string]any, refused bool) {
	t.Helper()
	result, _ := out["result"].(map[string]any)
	if refused || result["status"] != "queued" || result["evidence"] != string(surface.EvidenceQueued) {
		t.Fatalf("refused=%v out=%v, want the send queued for the host", refused, out)
	}
}

func TestASendToAPairedHostThatIsDownQueuesAndGoesOutOnceItAnswers(t *testing.T) {
	h := newHostTargets(t, false)
	h.goAway(t)
	out, refused := h.send(t, "vps::remote-a", "while the host is down", nil)
	queuedReceipt(t, out, refused)
	out, refused = h.send(t, "vps::remote-b", "to another project while down", nil)
	queuedReceipt(t, out, refused)
	if h.reached("while the host is down") {
		t.Fatal("delivered while the host was down")
	}
	h.comeBack(t)
	testkit.Eventually(t, "the queued send delivered once the host answers", 15*time.Second, func() bool { return h.reached("while the host is down") })
	testkit.Eventually(t, "the out-of-project send settled", 15*time.Second, func() bool {
		queued, err := h.local.reg.HostDeliveries(h.record.ID)
		return err == nil && len(queued) == 0
	})
	if h.reached("to another project while down") {
		t.Fatal("the owner delivered a send outside the sender's project")
	}
}

func TestAReportToAProjectLeadOnAHostThatIsDownIsHeldAndDeliveredOnceItAnswers(t *testing.T) {
	h := newHostTargets(t, false)
	h.goAway(t)
	out, refused := callMCP(t, h.caller, "agenthail_send", map[string]any{"project": widgetProject, "message": "report while down", "clientRequestId": "report-1"})
	queuedReceipt(t, out, refused)
	again, refused := callMCP(t, h.caller, "agenthail_send", map[string]any{"project": widgetProject, "message": "report while down", "clientRequestId": "report-1"})
	queuedReceipt(t, again, refused)
	if held, err := h.local.reg.HostHeldActions(h.record.ID); err != nil || len(held) != 1 || held[0].Status != registry.HostActionPending {
		t.Fatalf("held=%+v err=%v, want one report held and never sent", held, err)
	}
	h.comeBack(t)
	testkit.Eventually(t, "the held report delivered once the host answers", 15*time.Second, func() bool { return h.reached("report while down") })
	testkit.Eventually(t, "the held report settled", 15*time.Second, func() bool {
		held, err := h.local.reg.HostHeldActions(h.record.ID)
		return err == nil && len(held) == 0
	})
	delivered := 0
	for _, got := range h.remote.fake.delivered() {
		if strings.Contains(got, "report while down") {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered %d times, want once: %q", delivered, h.remote.fake.delivered())
	}
}

func (h hostTargets) listed(t *testing.T) map[string]map[string]any {
	t.Helper()
	out, refused := callMCP(t, h.caller, "agenthail_sessions", map[string]any{"filter": "project"})
	if refused {
		t.Fatalf("sessions: %v", out)
	}
	listed := map[string]map[string]any{}
	rows, _ := out["sessions"].([]any)
	for _, row := range rows {
		view := row.(map[string]any)
		listed[view["id"].(string)] = view
	}
	return listed
}

func TestSessionsListsAPairedHostsSessionsInTheCallersProject(t *testing.T) {
	h := newHostTargets(t, false)
	listed := h.listed(t)
	builder := listed[hosts.Qualify(h.record.ID, "remote-c")]
	host, _ := builder["host"].(map[string]any)
	if builder == nil || builder["alias"] != "builder" || host["name"] != "vps" || host["id"] != h.record.ID {
		t.Fatalf("listed=%v", listed)
	}
	if _, ok := listed[hosts.Qualify(h.record.ID, "remote-a")]; !ok {
		t.Fatalf("the paired host's lead is missing: %v", listed)
	}
	if _, ok := listed[hosts.Qualify(h.record.ID, "remote-b")]; ok {
		t.Fatalf("another project's session was listed: %v", listed)
	}
	if local := listed["local-a"]; local == nil || local["host"] != nil {
		t.Fatalf("the caller's own host's session: %v", local)
	}
	h.remote.gate.proxyDown.Store(true)
	h.remote.server.CloseClientConnections()
	testkit.Eventually(t, "the paired host's rows marked unavailable", 10*time.Second, func() bool {
		rows := sessionsOf(h.local.snapshot(t), h.record.ID)
		return len(rows) > 0 && !slices.ContainsFunc(rows, func(row dashboardSession) bool { return row.UnavailableReason == "" })
	})
	for id := range h.listed(t) {
		if _, _, remote := hosts.Split(id); remote {
			t.Fatalf("a session on an unreachable host was listed: %s", id)
		}
	}
}

func mustHostID(t *testing.T, node *federationNode) string {
	t.Helper()
	identity, err := node.reg.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return identity.ID
}

func TestAWorkerReachesItsSpawnerOnAPairedHostThatIsDownOnceItAnswers(t *testing.T) {
	h := newHostTargets(t, false)
	if err := h.local.reg.RecordSessionOrigin(registry.OriginOf("local-a", "remote-b")); err != nil {
		t.Fatal(err)
	}
	h.goAway(t)
	out, refused := h.send(t, "vps::remote-b", "to the spawner while down", nil)
	queuedReceipt(t, out, refused)
	h.comeBack(t)
	testkit.Eventually(t, "the send to the spawner delivered once the host answers", 15*time.Second, func() bool { return h.reached("to the spawner while down") })
}
