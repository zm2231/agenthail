package daemon

import (
	"encoding/json"
	"errors"
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
	if takeover.Code != http.StatusConflict || !strings.Contains(takeover.Body.String(), "@"+name) || apiErrorCode(t, takeover) != "coordinator_exists" {
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
	laptop := newFederationNode(t, "laptop-a")
	vps := newFederationNode(t, "vps-a", "vps-b")
	moveSessions(t, laptop, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget")), "src"))
	moveSessions(t, vps, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	laptop.serve(t, nil)
	vps.serve(t, nil)
	roots := testRoots(laptop.server)
	laptop.start(t, roots)
	vps.start(t, roots)
	vpsRecord := laptop.pair(t, vps, "vps")
	laptopRecord := vps.pair(t, laptop, "laptop")
	awaitFederation(t, "both mirrors", func() bool {
		return len(sessionsOf(laptop.snapshot(t), vpsRecord.ID)) == 2 && len(sessionsOf(vps.snapshot(t), laptopRecord.ID)) == 1
	})
	const project = "repo:github.com/example/gadget"

	set := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(vpsRecord.ID, "vps-a"), "name": "gadget-lead", "sourceSessionId": "laptop-a"})
	if set.Code != http.StatusOK {
		t.Fatalf("set from laptop: %d %s", set.Code, set.Body)
	}
	owned, err := vps.reg.Coordinator(project)
	if err != nil || owned.SessionID != "vps-a" || owned.ChangedBy != hosts.Qualify(laptopRecord.ID, "laptop-a") {
		t.Fatalf("vps coordinator=%+v err=%v", owned, err)
	}
	if _, err := laptop.reg.Coordinator(project); err == nil {
		t.Fatal("laptop wrote a coordinator for a vps conversation")
	}
	awaitFederation(t, "laptop mirrors vps's coordinator", func() bool {
		mirrored, _ := laptop.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == vpsRecord.ID && mirrored[0].SessionID == hosts.Qualify(vpsRecord.ID, "vps-a")
	})

	takeover := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(laptopRecord.ID, "laptop-a"), "name": "laptop-lead"})
	if takeover.Code != http.StatusConflict || !strings.Contains(takeover.Body.String(), "already has coordinator @gadget-lead;") {
		t.Fatalf("takeover from vps: %d %s", takeover.Code, takeover.Body)
	}
	replaced := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(laptopRecord.ID, "laptop-a"), "name": "laptop-lead", "replace": true, "sourceSessionId": "vps-b"})
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace from vps: %d %s", replaced.Code, replaced.Body)
	}
	if _, err := vps.reg.Coordinator(project); err == nil {
		t.Fatal("vps kept the coordinator laptop replaced")
	}
	history, err := vps.reg.CoordinatorHistory(project)
	if err != nil || len(history) != 2 || history[1].Action != "cleared" || history[1].ChangedBy != hosts.Qualify(vpsRecord.ID, "vps-b") {
		t.Fatalf("vps history=%+v err=%v", history, err)
	}
	lead, err := laptop.reg.Coordinator(project)
	if err != nil || lead.SessionID != "laptop-a" || lead.Name != "laptop-lead" || lead.ChangedBy != hosts.Qualify(vpsRecord.ID, "vps-b") {
		t.Fatalf("laptop coordinator=%+v err=%v", lead, err)
	}
	awaitFederation(t, "vps mirrors laptop's coordinator alone", func() bool {
		mirrored, _ := vps.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == laptopRecord.ID && mirrored[0].Name == "laptop-lead"
	})

	vps.stop()
	unreachable := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": hosts.Qualify(vpsRecord.ID, "vps-b"), "name": "vps-lead", "replace": true})
	if unreachable.Code == http.StatusOK || !strings.Contains(unreachable.Body.String(), "vps") {
		t.Fatalf("set on an unreachable owner: %d %s", unreachable.Code, unreachable.Body)
	}
	if current, err := laptop.reg.ProjectCoordinators(project); err != nil || len(current) != 1 || current[0].HostID != "" || current[0].Name != "laptop-lead" {
		t.Fatalf("laptop coordinators=%+v err=%v", current, err)
	}
}

func TestReplacingAPeerCoordinatorIsRefusedWhileItsHostIsUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	laptop := newFederationNode(t, "laptop-a")
	vps := newFederationNode(t, "vps-a")
	moveSessions(t, laptop, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget")), "src"))
	moveSessions(t, vps, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	vps.serve(t, nil)
	laptop.start(t, testRoots(vps.server))
	record := laptop.pair(t, vps, "vps")
	const project = "repo:github.com/example/gadget"
	if response := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "vps-a", "name": "gadget-lead"}); response.Code != http.StatusOK {
		t.Fatalf("set on vps: %d %s", response.Code, response.Body)
	}
	awaitFederation(t, "laptop mirrors vps's coordinator", func() bool {
		mirrored, _ := laptop.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == record.ID
	})
	if err := laptop.reg.RegisterSession(surface.Session{ID: "laptop-other", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: laptop.fake.sessions["laptop-a"].Cwd, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if err := laptop.reg.SetAlias("taken-lead", "laptop-other"); err != nil {
		t.Fatal(err)
	}
	clash := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "laptop-a", "name": "taken-lead", "replace": true})
	if clash.Code != http.StatusConflict || !strings.Contains(clash.Body.String(), "@taken-lead already names session laptop-other") {
		t.Fatalf("replace with a taken name: %d %s", clash.Code, clash.Body)
	}
	if owned, err := vps.reg.Coordinator(project); err != nil || owned.Name != "gadget-lead" {
		t.Fatalf("a refused replace cleared vps's coordinator: %+v err=%v", owned, err)
	}
	if _, err := laptop.reg.Coordinator(project); err == nil {
		t.Fatal("a refused replace wrote a local coordinator")
	}
	vps.stop()
	refused := laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "laptop-a", "name": "laptop-lead", "replace": true})
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "cannot replace vps::@gadget-lead") || !strings.Contains(refused.Body.String(), "nothing changed") {
		t.Fatalf("replace: %d %s", refused.Code, refused.Body)
	}
	if _, err := laptop.reg.Coordinator(project); err == nil {
		t.Fatal("laptop took over while vps could not confirm")
	}
	if alias, err := laptop.reg.LookupAlias("laptop-lead"); err == nil {
		t.Fatalf("a withdrawn replace left @laptop-lead on %s", alias)
	}
	if owned, err := vps.reg.Coordinator(project); err != nil || owned.Name != "gadget-lead" {
		t.Fatalf("vps coordinator=%+v err=%v", owned, err)
	}
}

func TestANameClaimedWhileAPeerClearsItsCoordinatorCannotStrandTheProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	upstream := catalogTestRepo(t)
	laptop := newFederationNode(t, "laptop-a", "laptop-other")
	vps := newFederationNode(t, "vps-a")
	moveSessions(t, laptop, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget")), "src"))
	moveSessions(t, vps, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
	vps.serve(t, nil)
	laptop.start(t, testRoots(vps.server))
	record := laptop.pair(t, vps, "vps")
	const project = "repo:github.com/example/gadget"
	if response := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "vps-a", "name": "gadget-lead"}); response.Code != http.StatusOK {
		t.Fatalf("set on vps: %d %s", response.Code, response.Body)
	}
	awaitFederation(t, "laptop mirrors vps's coordinator", func() bool {
		mirrored, _ := laptop.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == record.ID
	})

	hold := &snapshotHold{release: make(chan struct{})}
	vps.gate.actions.Store(hold)
	var once sync.Once
	release := func() {
		once.Do(func() {
			vps.gate.actions.Store(nil)
			close(hold.release)
		})
	}
	t.Cleanup(release)
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		answered <- laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "laptop-a", "name": "race-lead", "replace": true})
	}()
	awaitFederation(t, "the clear reaching vps", func() bool { return hold.waiting.Load() == 1 })
	if err := laptop.reg.SetAlias("race-lead", "laptop-other"); err == nil {
		t.Fatal("another session took the name while vps was clearing its coordinator")
	}
	release()
	if response := <-answered; response.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", response.Code, response.Body)
	}
	if lead, err := laptop.reg.Coordinator(project); err != nil || lead.Name != "race-lead" || lead.SessionID != "laptop-a" {
		t.Fatalf("laptop coordinator=%+v err=%v", lead, err)
	}
	if _, err := vps.reg.Coordinator(project); err == nil {
		t.Fatal("vps kept its coordinator after confirming the clear")
	}
}

func TestARefusedReplaceRestoresTheLocalCoordinatorOrKeepsTheNewOne(t *testing.T) {
	for _, renamed := range []bool{false, true} {
		t.Run(map[bool]string{false: "restores, and the new coordinator's session keeps its name meanwhile", true: "keeps the new one when the old name moved"}[renamed], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			upstream := catalogTestRepo(t)
			laptop := newFederationNode(t, "laptop-a", "laptop-other", "laptop-third")
			vps := newFederationNode(t, "vps-a")
			cwd := filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "laptop-gadget")), "src")
			moveSessions(t, laptop, cwd)
			moveSessions(t, vps, filepath.Join(cloneAt(t, upstream, filepath.Join(t.TempDir(), "vps-gadget")), "src"))
			vps.serve(t, nil)
			laptop.start(t, testRoots(vps.server))
			record := laptop.pair(t, vps, "vps")
			const project = "repo:github.com/example/gadget"
			if response := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "vps-a", "name": "peer-lead"}); response.Code != http.StatusOK {
				t.Fatalf("set on vps: %d %s", response.Code, response.Body)
			}
			awaitFederation(t, "laptop mirrors vps's coordinator", func() bool {
				mirrored, _ := laptop.reg.ProjectCoordinators(project)
				return len(mirrored) == 1 && mirrored[0].HostID == record.ID
			})
			old, err := laptop.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: project, ProjectRoot: cwd, Name: "old-lead", Surface: "codex", Cwd: cwd, Instructions: "brief"}, registry.CoordinatorChange{SessionID: "laptop-a", Replace: true, OverPeers: true, ChangedBy: "fixture"})
			if err != nil {
				t.Fatal(err)
			}

			hold := &snapshotHold{release: make(chan struct{})}
			vps.gate.actions.Store(hold)
			var once sync.Once
			release := func() {
				once.Do(func() {
					vps.gate.actions.Store(nil)
					close(hold.release)
				})
			}
			t.Cleanup(release)
			answered := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				answered <- laptop.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "laptop-other", "name": "new-lead", "replace": true})
			}()
			awaitFederation(t, "the clear reaching vps", func() bool { return hold.waiting.Load() == 1 })
			if response := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "coordinator-set", "sessionId": "vps-a", "name": "peer-lead-now", "replace": true}); response.Code != http.StatusOK {
				t.Fatalf("vps changes its coordinator: %d %s", response.Code, response.Body)
			}
			var aliasErr registry.CoordinatorAliasError
			if err := laptop.reg.SetAlias("renamed-other", "laptop-other"); !errors.As(err, &aliasErr) {
				t.Fatalf("the new coordinator's session was renamed during the replace: %v", err)
			}
			if renamed {
				if err := laptop.reg.SetAlias("renamed", "laptop-a"); err != nil {
					t.Fatal(err)
				}
				if err := laptop.reg.SetAlias("old-lead", "laptop-third"); err != nil {
					t.Fatal(err)
				}
			}
			release()
			response := <-answered
			if response.Code != http.StatusConflict {
				t.Fatalf("replace: %d %s", response.Code, response.Body)
			}
			current, err := laptop.reg.Coordinator(project)
			if err != nil {
				t.Fatalf("the project lost its local coordinator: %v (%s)", err, response.Body)
			}
			if renamed {
				if current.Name != "new-lead" || current.SessionID != "laptop-other" || !strings.Contains(response.Body.String(), "stays here") {
					t.Fatalf("coordinator=%+v body=%s", current, response.Body)
				}
				return
			}
			if current.Name != "old-lead" || current.SessionID != "laptop-a" || current.Instructions != "brief" || !current.ChangedAt.Equal(old.ChangedAt) || !strings.Contains(response.Body.String(), "nothing changed") {
				t.Fatalf("coordinator=%+v body=%s", current, response.Body)
			}
			if alias, err := laptop.reg.ReverseAlias("laptop-other"); err == nil && alias != "" {
				t.Fatalf("laptop-other kept @%s", alias)
			}
		})
	}
}
