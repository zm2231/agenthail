package daemon

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCoordinatorSetRefusesATakeoverAndNamesWhoChangedIt(t *testing.T) {
	d, r, _, repo := projectsFixture(t)
	if response := runDashboardAction(t, d, `{"action":"project-add","path":"`+repo+`","title":"`+strings.Repeat("archive-", 12)+`"}`); response.Code != http.StatusOK {
		t.Fatalf("add: %d %s", response.Code, response.Body)
	}
	set := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","sourceSessionId":"to"}`)
	var answer struct {
		Coordinator registry.CoordinatorSpec `json:"coordinator"`
	}
	if set.Code != http.StatusOK || json.Unmarshal(set.Body.Bytes(), &answer) != nil {
		t.Fatalf("set: %d %s", set.Code, set.Body)
	}
	name := answer.Coordinator.Name
	if len(name) > 80 || !strings.HasSuffix(name, "-lead") || strings.Contains(name, "--") || answer.Coordinator.SessionID != "from" {
		t.Fatalf("coordinator=%+v", answer.Coordinator)
	}
	if want := hosts.Qualify(d.hostIdentity().ID, "to"); answer.Coordinator.ChangedBy != want {
		t.Fatalf("changedBy=%q want %q", answer.Coordinator.ChangedBy, want)
	}

	takeover := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"to","name":"other-lead"}`)
	if takeover.Code != http.StatusConflict || !strings.Contains(takeover.Body.String(), "@"+name) || !strings.Contains(takeover.Body.String(), "--replace") {
		t.Fatalf("takeover: %d %s", takeover.Code, takeover.Body)
	}
	if current, _ := r.Coordinator(answer.Coordinator.ProjectID); current.SessionID != "from" {
		t.Fatalf("a refused takeover moved the coordinator: %+v", current)
	}
	if replaced := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"to","name":"other-lead","replace":true}`); replaced.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", replaced.Code, replaced.Body)
	}
	history, err := r.CoordinatorHistory(answer.Coordinator.ProjectID)
	if err != nil || len(history) != 2 || history[1].Action != "replaced" || history[1].PreviousName != name || history[1].ChangedBy == "" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-set","sessionId":"from","name":"other-lead","permissionMode":"plan","replace":true}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "claude coordinator only") {
		t.Fatalf("codex permission mode: %d %s", response.Code, response.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"coordinator-new","surface":"codex","path":"`+filepath.Join(t.TempDir())+`"}`); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "not in a project") {
		t.Fatalf("outside a project: %d %s", response.Code, response.Body)
	}
}

func TestCoordinatorSetRunsOnTheConversationsHostInBothDirections(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	mac := newFederationNode(t, "mac-a")
	forge := newFederationNode(t, "forge-a", "forge-b")
	moveSessions(t, mac, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "mac-gadget")), "src"))
	moveSessions(t, forge, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "forge-gadget")), "src"))
	mac.serve(t, nil)
	forge.serve(t, nil)
	roots := testRoots(mac.server)
	mac.start(t, roots)
	forge.start(t, roots)
	forgeRecord := mac.pair(t, forge, "forge")
	macRecord := forge.pair(t, mac, "mac")
	awaitFederation(t, "both mirrors", func() bool {
		return len(sessionsOf(mac.snapshot(t), forgeRecord.ID)) == 2 && len(sessionsOf(forge.snapshot(t), macRecord.ID)) == 1
	})
	const project = "repo:github.com/example/gadget"

	set := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(forgeRecord.ID, "forge-a"), "name": "gadget-lead", "sourceSessionId": "mac-a"})
	if set.Code != http.StatusOK {
		t.Fatalf("set from mac: %d %s", set.Code, set.Body)
	}
	owned, err := forge.reg.Coordinator(project)
	if err != nil || owned.SessionID != "forge-a" || owned.ChangedBy != hosts.Qualify(macRecord.ID, "mac-a") {
		t.Fatalf("forge coordinator=%+v err=%v", owned, err)
	}
	if _, err := mac.reg.Coordinator(project); err == nil {
		t.Fatal("mac wrote a coordinator for a forge conversation")
	}
	awaitFederation(t, "mac mirrors forge's coordinator", func() bool {
		mirrored, _ := mac.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == forgeRecord.ID && mirrored[0].SessionID == hosts.Qualify(forgeRecord.ID, "forge-a")
	})

	takeover := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(macRecord.ID, "mac-a"), "name": "mac-lead"})
	if takeover.Code != http.StatusConflict || !strings.Contains(takeover.Body.String(), "already has coordinator @gadget-lead;") {
		t.Fatalf("takeover from forge: %d %s", takeover.Code, takeover.Body)
	}
	replaced := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(macRecord.ID, "mac-a"), "name": "mac-lead", "replace": true, "sourceSessionId": "forge-b"})
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace from forge: %d %s", replaced.Code, replaced.Body)
	}
	if _, err := forge.reg.Coordinator(project); err == nil {
		t.Fatal("forge kept the coordinator mac replaced")
	}
	history, err := forge.reg.CoordinatorHistory(project)
	if err != nil || len(history) != 2 || history[1].Action != "cleared" || history[1].ChangedBy != hosts.Qualify(forgeRecord.ID, "forge-b") {
		t.Fatalf("forge history=%+v err=%v", history, err)
	}
	lead, err := mac.reg.Coordinator(project)
	if err != nil || lead.SessionID != "mac-a" || lead.Name != "mac-lead" || lead.ChangedBy != hosts.Qualify(forgeRecord.ID, "forge-b") {
		t.Fatalf("mac coordinator=%+v err=%v", lead, err)
	}
	awaitFederation(t, "forge mirrors mac's coordinator alone", func() bool {
		mirrored, _ := forge.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == macRecord.ID && mirrored[0].Name == "mac-lead"
	})

	forge.stop()
	unreachable := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(forgeRecord.ID, "forge-b"), "name": "forge-lead", "replace": true})
	if unreachable.Code == http.StatusOK || !strings.Contains(unreachable.Body.String(), "forge") {
		t.Fatalf("set on an unreachable owner: %d %s", unreachable.Code, unreachable.Body)
	}
	if current, err := mac.reg.ProjectCoordinators(project); err != nil || len(current) != 1 || current[0].HostID != "" || current[0].Name != "mac-lead" {
		t.Fatalf("mac coordinators=%+v err=%v", current, err)
	}
}

func TestReplacingAPeerCoordinatorIsRefusedWhileItsHostIsUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	mac := newFederationNode(t, "mac-a")
	forge := newFederationNode(t, "forge-a")
	moveSessions(t, mac, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "mac-gadget")), "src"))
	moveSessions(t, forge, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "forge-gadget")), "src"))
	forge.serve(t, nil)
	mac.start(t, testRoots(forge.server))
	record := mac.pair(t, forge, "forge")
	const project = "repo:github.com/example/gadget"
	if response := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "forge-a", "name": "gadget-lead"}); response.Code != http.StatusOK {
		t.Fatalf("set on forge: %d %s", response.Code, response.Body)
	}
	awaitFederation(t, "mac mirrors forge's coordinator", func() bool {
		mirrored, _ := mac.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == record.ID
	})
	if err := mac.reg.RegisterSession(surface.Session{ID: "mac-other", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: mac.fake.sessions["mac-a"].Cwd, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if err := mac.reg.SetAlias("taken-lead", "mac-other"); err != nil {
		t.Fatal(err)
	}
	clash := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "mac-a", "name": "taken-lead", "replace": true})
	if clash.Code != http.StatusConflict || !strings.Contains(clash.Body.String(), "@taken-lead already names session mac-other") {
		t.Fatalf("replace with a taken name: %d %s", clash.Code, clash.Body)
	}
	if owned, err := forge.reg.Coordinator(project); err != nil || owned.Name != "gadget-lead" {
		t.Fatalf("a refused replace cleared forge's coordinator: %+v err=%v", owned, err)
	}
	if _, err := mac.reg.Coordinator(project); err == nil {
		t.Fatal("a refused replace wrote a local coordinator")
	}
	forge.stop()
	refused := mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "mac-a", "name": "mac-lead", "replace": true})
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "cannot replace forge::@gadget-lead") || !strings.Contains(refused.Body.String(), "nothing changed") {
		t.Fatalf("replace: %d %s", refused.Code, refused.Body)
	}
	if _, err := mac.reg.Coordinator(project); err == nil {
		t.Fatal("mac took over while forge could not confirm")
	}
	if owned, err := forge.reg.Coordinator(project); err != nil || owned.Name != "gadget-lead" {
		t.Fatalf("forge coordinator=%+v err=%v", owned, err)
	}
}
