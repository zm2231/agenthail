package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type joinFixture struct {
	mac, server             *federationNode
	serverRecord, macRecord registry.PairedHost
	macFolder, serverFolder string
	projectID               string
}

// A folder on the Mac with no repository, and a clone on the server whose
// only remote is a path on that machine: neither has a repository key.
func newJoinFixture(t *testing.T) joinFixture {
	t.Helper()
	mac, server, serverRecord, macRecord := pairedBothWays(t)
	f := joinFixture{mac: mac, server: server, serverRecord: serverRecord, macRecord: macRecord, macFolder: t.TempDir()}
	f.serverFolder = testkit.GitRepo(t)
	testkit.Git(t, f.serverFolder, "remote", "add", "origin", filepath.Join(t.TempDir(), "bundle"))
	moveSession(t, mac, "laptop-a", f.macFolder, time.Now())
	moveSession(t, server, "vps-a", f.serverFolder, time.Now())
	var added struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	f.action(t, mac, map[string]any{"action": "project-add", "path": f.macFolder, "title": "agenthail-v2"}, http.StatusOK, &added)
	f.projectID = added.Project.ID
	return f
}

func (f joinFixture) action(t *testing.T, node *federationNode, body map[string]any, status int, into any) string {
	t.Helper()
	response := node.request(t, http.MethodPost, "/api/v1/actions", body)
	if response.Code != status {
		t.Fatalf("%v: status=%d body=%s, want %d", body, response.Code, response.Body.String(), status)
	}
	if into != nil {
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
	return response.Body.String()
}

func (f joinFixture) refused(t *testing.T, node *federationNode, body map[string]any, status int, code string) {
	t.Helper()
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	f.action(t, node, body, status, &refusal)
	if refusal.Error.Code != code {
		t.Fatalf("%v: refused with %q, want %q", body, refusal.Error.Code, code)
	}
}

func projectNamed(state dashboardState, id string) *dashboardProject {
	for index := range state.Projects {
		if state.Projects[index].ID == id {
			return &state.Projects[index]
		}
	}
	return nil
}

func rowProject(state dashboardState, sessionID string) string {
	for _, row := range state.Sessions {
		if row.ID == sessionID && row.Project != nil {
			return row.Project.ID
		}
	}
	return ""
}

func discoverOn(nodes ...*federationNode) {
	for _, node := range nodes {
		node.daemon.discoverCatalog(context.Background())
	}
}

func TestAFolderOnAnotherComputerJoinsAProjectWithOneKeyLeadAndLedger(t *testing.T) {
	f := newJoinFixture(t)
	if _, err := f.mac.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: f.projectID, ProjectRoot: f.macFolder, Name: "v2-lead", Surface: "codex", Cwd: f.macFolder}, registry.CoordinatorChange{SessionID: "laptop-a", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	macLedger := ledger.NewStore(f.mac.reg.WriteDB(), f.mac.reg.ReadDB())
	if err := macLedger.Enable(registry.LedgerProjectKey(f.projectID), ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}

	var joined struct {
		Project struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"project"`
		Created bool `json:"created"`
	}
	f.action(t, f.mac, map[string]any{"action": "project-add", "path": f.serverFolder, "host": "vps", "join": f.projectID}, http.StatusOK, &joined)
	key := joined.Project.ID
	if !strings.HasPrefix(key, hosts.SharedPrefix) || !joined.Created || joined.Project.DisplayName != "agenthail-v2" {
		t.Fatalf("the join answered %+v, want a new checkout under a shared key named agenthail-v2", joined)
	}

	testkit.Eventually(t, "one project with both folders and the Mac's lead on both computers", 10*time.Second, func() bool {
		discoverOn(f.server, f.mac)
		for _, node := range []*federationNode{f.mac, f.server} {
			state := node.snapshot(t)
			project := projectNamed(state, key)
			if project == nil || len(project.Checkouts) != 2 || len(project.Coordinators) != 1 || project.Coordinators[0].Name != "v2-lead" || project.DisplayName != "agenthail-v2" {
				return false
			}
			if rowProject(state, "vps-a") != key && rowProject(state, hosts.Qualify(f.serverRecord.ID, "vps-a")) != key {
				return false
			}
		}
		return true
	})
	if state := f.server.snapshot(t); projectNamed(state, f.projectID) != nil || projectNamed(state, hosts.Qualify(f.macRecord.ID, f.projectID)) != nil {
		t.Fatal("the server still lists the Mac's project under its own id")
	}

	if held, err := f.mac.reg.HoldsLedger(key); err != nil || !held {
		t.Fatalf("the Mac's ledger did not move to the shared key: held=%v err=%v", held, err)
	}
	if held, err := f.mac.reg.HoldsLedger(registry.LedgerProjectKey(f.projectID)); err != nil || held {
		t.Fatalf("a ledger stayed under the Mac's own id: held=%v err=%v", held, err)
	}
	owner, _, err := ledger.NewStore(f.server.reg.WriteDB(), f.server.reg.ReadDB()).Owner(key)
	if err != nil || owner != f.macRecord.ID {
		t.Fatalf("the server's ledger of the project is kept by %q (%v), want the Mac", owner, err)
	}
	if lead, err := f.mac.reg.LocalCoordinators(); err != nil || len(lead) != 1 || lead[0].ProjectID != key {
		t.Fatalf("the Mac's lead is held under %+v (%v), want the shared key", lead, err)
	}

	worker, err := f.server.reg.Session("vps-a")
	if err != nil {
		t.Fatal(err)
	}
	client := mcpSession(t, mcpServer(t, f.server.daemon), issueMCPToken(t, f.server.daemon, *worker))
	if out, refused := callMCP(t, client, "agenthail_send", map[string]any{"target": "@lead", "message": "report from the server", "clientRequestId": "report-1"}); refused {
		t.Fatalf("@lead from the server's folder: %v", out)
	}
	testkit.Eventually(t, "the server folder's @lead reaching the Mac's lead", 10*time.Second, func() bool {
		for _, message := range f.mac.fake.delivered() {
			if strings.Contains(message, "report from the server") {
				return true
			}
		}
		return false
	})

	f.action(t, f.mac, map[string]any{"action": "project-rename", "projectId": key, "title": "cockpit"}, http.StatusOK, nil)
	testkit.Eventually(t, "the rename on both computers under the same key", 10*time.Second, func() bool {
		discoverOn(f.server, f.mac)
		mac, server := projectNamed(f.mac.snapshot(t), key), projectNamed(f.server.snapshot(t), key)
		return mac != nil && server != nil && mac.DisplayName == "cockpit" && server.DisplayName == "cockpit"
	})

	records, err := f.server.reg.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	checkout := records[slices.IndexFunc(records, func(record registry.ProjectRecord) bool { return record.Joined })]
	stale := checkout
	stale.Joined, stale.Key, stale.Repository = false, "", nil
	beforeJoin := []registry.ProjectRecord{stale}
	testkit.Git(t, f.serverFolder, "remote", "set-url", "origin", "https://github.com/example/other.git")
	f.server.daemon.refreshProjectRepositories(context.Background(), beforeJoin, time.Now())
	testkit.Git(t, f.serverFolder, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "bundle"))
	if beforeJoin[0].Key != "" {
		t.Fatalf("a refresh read before the join gave the joined folder the key %q", beforeJoin[0].Key)
	}
	if records, err := f.server.reg.ListProjects(); err != nil || !slices.ContainsFunc(records, func(record registry.ProjectRecord) bool { return record.Joined && record.Key == key }) {
		t.Fatalf("a repository refresh replaced the joined folder's key: %+v (%v)", records, err)
	}

	again := f.action(t, f.mac, map[string]any{"action": "project-add", "path": f.serverFolder, "host": "vps", "join": key}, http.StatusOK, nil)
	if !strings.Contains(again, `"created":false`) {
		t.Fatalf("joining the same folder again = %s, want created false", again)
	}

	var serverCheckout string
	for _, checkout := range projectNamed(f.mac.snapshot(t), key).Checkouts {
		if checkout.Host.ID == f.serverRecord.ID {
			serverCheckout = checkout.ProjectID
		}
	}
	f.action(t, f.mac, map[string]any{"action": "project-unjoin", "projectId": serverCheckout}, http.StatusOK, nil)
	testkit.Eventually(t, "the server's folder a project of its own again, the Mac's keeping the key and lead", 10*time.Second, func() bool {
		discoverOn(f.server, f.mac)
		server := f.server.snapshot(t)
		own := rowProject(server, "vps-a")
		mac := projectNamed(f.mac.snapshot(t), key)
		return strings.HasPrefix(own, "p-") && mac != nil && len(mac.Checkouts) == 1 && len(mac.Coordinators) == 1
	})
}

func TestAJoinIsRefusedWhereTheFolderAlreadyBelongsElsewhere(t *testing.T) {
	f := newJoinFixture(t)

	keyed := widgetRepo(t, "https://github.com/example/widget.git")
	f.refused(t, f.mac, map[string]any{"action": "project-add", "path": keyed, "host": "vps", "join": f.projectID}, http.StatusConflict, "project_keyed")

	gainedRemote := testkit.GitRepo(t)
	f.action(t, f.server, map[string]any{"action": "project-add", "path": gainedRemote}, http.StatusOK, nil)
	testkit.Git(t, gainedRemote, "remote", "add", "origin", "https://github.com/example/gadget.git")
	f.refused(t, f.mac, map[string]any{"action": "project-add", "path": gainedRemote, "host": "vps", "join": f.projectID}, http.StatusConflict, "project_keyed")
	if synced, err := ledger.NewStore(f.server.reg.WriteDB(), f.server.reg.ReadDB()).SyncedProjects(f.macRecord.ID); err != nil || len(synced) != 0 {
		t.Fatalf("a refused join left the server replicating %v (%v)", synced, err)
	}

	serverRoot, err := projectresolveRoot(f.serverFolder)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: "implicit:" + serverRoot, ProjectRoot: serverRoot, Name: "server-lead", Surface: "codex", Cwd: serverRoot}, registry.CoordinatorChange{SessionID: "vps-a", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	f.refused(t, f.mac, map[string]any{"action": "project-add", "path": f.serverFolder, "host": "vps", "join": f.projectID}, http.StatusConflict, "project_history")
	assertNotAdded(t, f.server, serverRoot)

	f.server.gate.proxyDown.Store(true)
	other := t.TempDir()
	f.refused(t, f.mac, map[string]any{"action": "project-add", "path": other, "host": "vps", "join": f.projectID}, http.StatusBadGateway, "host_unreachable")
	f.server.gate.proxyDown.Store(false)
	assertNotAdded(t, f.server, other)

	second := t.TempDir()
	f.action(t, f.server, map[string]any{"action": "project-add", "path": second, "title": "elsewhere"}, http.StatusOK, nil)
	f.action(t, f.mac, map[string]any{"action": "project-add", "path": other, "host": "vps", "join": f.projectID}, http.StatusOK, nil)
	var elsewhere string
	records, err := f.server.reg.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Title == "elsewhere" {
			elsewhere = record.ID
		}
	}
	f.refused(t, f.server, map[string]any{"action": "project-add", "path": other, "join": elsewhere}, http.StatusConflict, "project_joined")
}

func projectresolveRoot(path string) (string, error) { return filepath.EvalSymlinks(path) }

func assertNotAdded(t *testing.T, node *federationNode, folder string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatal(err)
	}
	records, err := node.reg.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Added && record.WorkspaceRoot == root {
			t.Fatalf("a refused join still added %s: %+v", folder, record)
		}
	}
}

func TestAJoinAndAPlainAddOfTheSameFolderEndWithTheFolderJoined(t *testing.T) {
	f := newJoinFixture(t)
	folder := t.TempDir()
	f.server.daemon.projectWrites.Lock()
	answers := make(chan int, 2)
	go func() {
		answers <- f.mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "project-add", "path": folder, "host": "vps", "join": f.projectID}).Code
	}()
	go func() {
		answers <- f.server.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "project-add", "path": folder}).Code
	}()
	time.Sleep(200 * time.Millisecond)
	f.server.daemon.projectWrites.Unlock()
	for range 2 {
		if code := <-answers; code != http.StatusOK {
			t.Fatalf("an add or join answered %d", code)
		}
	}
	root, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatal(err)
	}
	records, err := f.server.reg.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Added && record.WorkspaceRoot == root {
			if !record.Joined || !strings.HasPrefix(record.Key, hosts.SharedPrefix) {
				t.Fatalf("the folder ended as %+v, want it joined under the shared key", record)
			}
			return
		}
	}
	t.Fatal("the folder was not added")
}

func TestARemovalDuringAJoinOfTheSameFolderWaitsForIt(t *testing.T) {
	f := newJoinFixture(t)
	folder := t.TempDir()
	var added struct {
		Project struct {
			LocalID string `json:"localId"`
		} `json:"project"`
	}
	f.action(t, f.server, map[string]any{"action": "project-add", "path": folder}, http.StatusOK, &added)
	f.server.daemon.projectWrites.Lock()
	answers := make(chan string, 2)
	go func() {
		response := f.mac.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "project-add", "path": folder, "host": "vps", "join": f.projectID})
		answers <- "join " + response.Body.String()
	}()
	go func() {
		response := f.server.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "project-remove", "projectId": added.Project.LocalID})
		answers <- "remove " + response.Body.String()
	}()
	time.Sleep(200 * time.Millisecond)
	f.server.daemon.projectWrites.Unlock()
	for range 2 {
		if answer := <-answers; !strings.Contains(answer, `"ok":true`) {
			t.Fatalf("a join and a removal of one folder, one after the other, both stand; got %s", answer)
		}
	}
}

func allSessionsView(t *testing.T, node *federationNode) SessionViewSnapshot {
	t.Helper()
	response := node.request(t, http.MethodGet, "/api/v1/snapshot?scope=all&limit=200", nil)
	var view SessionViewSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || view.View == nil {
		t.Fatalf("session view: %v %s", err, response.Body.String())
	}
	return view
}

func TestAJoinMovesSessionsThatEndedBeforeIt(t *testing.T) {
	f := newJoinFixture(t)
	for node, id := range map[*federationNode]string{f.mac: "laptop-ended", f.server: "vps-b"} {
		node.fake.sessions[id] = surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work " + id, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: filepath.Join(t.TempDir(), id+".jsonl")}
	}
	moveSession(t, f.mac, "laptop-ended", f.macFolder, time.Now().Add(-time.Hour))
	moveSession(t, f.server, "vps-b", filepath.Join(f.serverFolder, "src"), time.Now().Add(-time.Hour))
	discoverOn(f.mac, f.server)
	f.mac.fake.end("laptop-ended")
	f.server.fake.end("vps-b")
	for range catalogOmissionThreshold + 1 {
		discoverOn(f.mac, f.server)
	}

	var joined struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	f.action(t, f.mac, map[string]any{"action": "project-add", "path": f.serverFolder, "host": "vps", "join": f.projectID}, http.StatusOK, &joined)
	key := joined.Project.ID

	ended := map[*federationNode][]string{
		f.mac:    {"laptop-ended", hosts.Qualify(f.serverRecord.ID, "vps-b")},
		f.server: {"vps-b", hosts.Qualify(f.macRecord.ID, "laptop-ended")},
	}
	testkit.Eventually(t, "the ended sessions in the shared project on both computers", 10*time.Second, func() bool {
		discoverOn(f.server, f.mac)
		for node, ids := range ended {
			projects := map[string]string{}
			for _, row := range allSessionsView(t, node).Sessions {
				if row.Project != nil {
					projects[row.ID] = row.Project.ID
				}
			}
			for _, id := range ids {
				if projects[id] != key {
					return false
				}
			}
		}
		return true
	})
	for node, name := range map[*federationNode]string{f.mac: "mac", f.server: "server"} {
		var named []string
		for _, project := range allSessionsView(t, node).View.Projects {
			if project.Name == "agenthail-v2" {
				named = append(named, project.ID)
			}
		}
		for _, project := range node.snapshot(t).Projects {
			if project.DisplayName == "agenthail-v2" && project.ID != key {
				named = append(named, project.ID)
			}
		}
		if !slices.Equal(named, []string{key}) {
			t.Fatalf("%s names agenthail-v2 %v, want only %s", name, named, key)
		}
	}
}

func TestAnEndedSessionWhoseRepositoryIsGoneKeepsItsProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	node := newFederationNode(t)
	repo := testkit.GitRepo(t)
	node.fake.sessions["gone"] = surface.Session{ID: "gone", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work gone", Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: filepath.Join(t.TempDir(), "gone.jsonl")}
	moveSession(t, node, "gone", repo, time.Now().Add(-time.Hour))
	discoverOn(node)
	node.fake.end("gone")
	for range catalogOmissionThreshold + 1 {
		discoverOn(node)
	}
	before := projectresolve.StoredSessionProject(node.reg, "gone")
	if before == nil || !strings.HasPrefix(before.ID, "implicit:") {
		t.Fatalf("the ended session's project before = %+v, want its repository", before)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	response := node.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "project-add", "path": t.TempDir(), "title": "elsewhere"})
	if response.Code != http.StatusOK {
		t.Fatalf("project-add: %d %s", response.Code, response.Body.String())
	}
	discoverOn(node)
	generation := node.daemon.catalogIdentities.Generation()
	testkit.Eventually(t, "the sweep after the add", 10*time.Second, func() bool {
		discoverOn(node)
		return node.daemon.catalogReidentified.Load() == generation
	})
	if after := projectresolve.StoredSessionProject(node.reg, "gone"); after == nil || after.ID != before.ID {
		t.Fatalf("the ended session's project after its repository went = %+v, want %s", after, before.ID)
	}
}
