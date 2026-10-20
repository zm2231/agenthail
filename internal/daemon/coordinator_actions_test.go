package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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
	if alias, err := mac.reg.LookupAlias("mac-lead"); err == nil {
		t.Fatalf("a withdrawn replace left @mac-lead on %s", alias)
	}
	if owned, err := forge.reg.Coordinator(project); err != nil || owned.Name != "gadget-lead" {
		t.Fatalf("forge coordinator=%+v err=%v", owned, err)
	}
}

func TestANameClaimedWhileAPeerClearsItsCoordinatorCannotStrandTheProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	mac := newFederationNode(t, "mac-a", "mac-other")
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

	hold := &snapshotHold{release: make(chan struct{})}
	forge.gate.actions.Store(hold)
	var once sync.Once
	release := func() {
		once.Do(func() {
			forge.gate.actions.Store(nil)
			close(hold.release)
		})
	}
	t.Cleanup(release)
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answered <- mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "mac-a", "name": "race-lead", "replace": true})
	}()
	awaitFederation(t, "the clear reaching forge", func() bool { return hold.waiting.Load() == 1 })
	if err := mac.reg.SetAlias("race-lead", "mac-other"); err == nil {
		t.Fatal("another session took the name while forge was clearing its coordinator")
	}
	release()
	if response := <-answered; response.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", response.Code, response.Body)
	}
	if lead, err := mac.reg.Coordinator(project); err != nil || lead.Name != "race-lead" || lead.SessionID != "mac-a" {
		t.Fatalf("mac coordinator=%+v err=%v", lead, err)
	}
	if _, err := forge.reg.Coordinator(project); err == nil {
		t.Fatal("forge kept its coordinator after confirming the clear")
	}
}

func TestARefusedReplaceRestoresTheLocalCoordinatorOrKeepsTheNewOne(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "restores", true: "keeps the new one when the old name moved"}[renamed], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			upstream := catalogTestRepo(t)
			mac := newFederationNode(t, "mac-a", "mac-other", "mac-third")
			forge := newFederationNode(t, "forge-a")
			cwd := filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "mac-gadget")), "src")
			moveSessions(t, mac, cwd)
			moveSessions(t, forge, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "forge-gadget")), "src"))
			forge.serve(t, nil)
			mac.start(t, testRoots(forge.server))
			record := mac.pair(t, forge, "forge")
			const project = "repo:github.com/example/gadget"
			if response := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "forge-a", "name": "peer-lead"}); response.Code != http.StatusOK {
				t.Fatalf("set on forge: %d %s", response.Code, response.Body)
			}
			awaitFederation(t, "mac mirrors forge's coordinator", func() bool {
				mirrored, _ := mac.reg.ProjectCoordinators(project)
				return len(mirrored) == 1 && mirrored[0].HostID == record.ID
			})
			old, err := mac.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project, ProjectRoot: cwd, Name: "old-lead", Surface: "codex", Cwd: cwd, Instructions: "brief"}, registry.CoordinatorChange{SessionID: "mac-a", Replace: true, OverPeers: true, ChangedBy: "fixture"})
			if err != nil {
				t.Fatal(err)
			}

			hold := &snapshotHold{release: make(chan struct{})}
			forge.gate.actions.Store(hold)
			var once sync.Once
			release := func() {
				once.Do(func() {
					forge.gate.actions.Store(nil)
					close(hold.release)
				})
			}
			t.Cleanup(release)
			answered := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				answered <- mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "mac-other", "name": "new-lead", "replace": true})
			}()
			awaitFederation(t, "the clear reaching forge", func() bool { return hold.waiting.Load() == 1 })
			if response := forge.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "forge-a", "name": "peer-lead", "replace": true}); response.Code != http.StatusOK {
				t.Fatalf("forge changes its coordinator: %d %s", response.Code, response.Body)
			}
			if renamed {
				if err := mac.reg.SetAlias("renamed", "mac-a"); err != nil {
					t.Fatal(err)
				}
				if err := mac.reg.SetAlias("old-lead", "mac-third"); err != nil {
					t.Fatal(err)
				}
			}
			release()
			response := <-answered
			if response.Code != http.StatusConflict {
				t.Fatalf("replace: %d %s", response.Code, response.Body)
			}
			current, err := mac.reg.Coordinator(project)
			if err != nil {
				t.Fatalf("the project lost its local coordinator: %v (%s)", err, response.Body)
			}
			if renamed {
				if current.Name != "new-lead" || current.SessionID != "mac-other" || !strings.Contains(response.Body.String(), "stays here") {
					t.Fatalf("coordinator=%+v body=%s", current, response.Body)
				}
				return
			}
			if current.Name != "old-lead" || current.SessionID != "mac-a" || current.Instructions != "brief" || !current.ChangedAt.Equal(old.ChangedAt) || !strings.Contains(response.Body.String(), "nothing changed") {
				t.Fatalf("coordinator=%+v body=%s", current, response.Body)
			}
			if alias, err := mac.reg.ReverseAlias("mac-other"); err == nil && alias != "" {
				t.Fatalf("mac-other kept @%s", alias)
			}
		})
	}
}
