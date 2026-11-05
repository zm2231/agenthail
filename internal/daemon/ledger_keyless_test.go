package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func addFolderProject(t *testing.T, d *Daemon, path string) string {
	t.Helper()
	response := runDashboardAction(t, d, `{"action":"project-add","path":"`+path+`"}`)
	var answer struct {
		Project CatalogProject `json:"project"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Project.LocalID == "" {
		t.Fatalf("project-add %s: %d %s", path, response.Code, response.Body)
	}
	return answer.Project.LocalID
}

func plainFolder(t *testing.T) string {
	t.Helper()
	folder := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalCatalogPath(folder)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func folderSession(t *testing.T, r *registry.Registry, id, cwd string) surface.Session {
	t.Helper()
	s := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd, Status: surface.StatusIdle}
	if err := r.RegisterSession(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func ledgerKeyAt(t *testing.T, r *registry.Registry, path string) string {
	t.Helper()
	key, err := ResolvePathLedgerKey(context.Background(), r, path)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func utteranceTexts(t *testing.T, store *ledger.Store, key string) []string {
	t.Helper()
	events, err := store.Events(key, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range withoutCapture(events) {
		var u ledger.UtterancePayload
		if err := json.Unmarshal(e.Payload, &u); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, u.Text)
	}
	return texts
}

func TestAFolderWithoutAUsableRemoteKeepsALedgerUnderItsProjectID(t *testing.T) {
	for name, folder := range map[string]func(t *testing.T) string{
		"no git": plainFolder,
		"file remote": func(t *testing.T) string {
			repo := catalogTestRepo(t)
			gitRun(t, repo, "remote", "add", "origin", "/srv/git/notes.git")
			return repo
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, r, handler := ledgerDaemon(t)
			path := folder(t)
			id := addFolderProject(t, d, path)
			key := ledgerKeyAt(t, r, path)
			if key != registry.LedgerProjectKey(id) {
				t.Fatalf("ledger key = %q, want the key of project %s", key, id)
			}
			store := ledger.NewStore(r.WriteDB(), r.ReadDB())
			if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
				t.Fatal(err)
			}
			s := folderSession(t, r, "worker", path)
			if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p1", Prompt: "keep notes"}); !resp.Captured || resp.ProjectKey != key {
				t.Fatalf("capture = %+v, want it in %s", resp, key)
			}
			if texts := utteranceTexts(t, store, key); len(texts) != 1 {
				t.Fatalf("ledger holds %q", texts)
			}
		})
	}
}

func TestAKeylessLedgerStaysPutWhenItsFolderGainsARemote(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	path := catalogTestRepo(t)
	gitRun(t, path, "remote", "add", "origin", "/srv/git/notes.git")
	id := addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	s := folderSession(t, r, "worker", path)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p1", Prompt: "before the remote"})

	gitRun(t, path, "remote", "set-url", "origin", "https://github.com/example/notes.git")
	records, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	d.refreshProjectRepositories(context.Background(), records, time.Now())
	if project, err := ResolvePathProject(context.Background(), r, path); err != nil || project.Key != "repo:github.com/example/notes" {
		t.Fatalf("the project did not take its new remote: %+v %v", project, err)
	}

	if got := ledgerKeyAt(t, r, path); got != registry.LedgerProjectKey(id) {
		t.Fatalf("ledger key after the remote = %q, want %s", got, key)
	}
	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p2", Prompt: "after the remote"}); resp.ProjectKey != key {
		t.Fatalf("capture after the remote went to %q", resp.ProjectKey)
	}
	if texts := utteranceTexts(t, store, key); len(texts) != 2 {
		t.Fatalf("ledger holds %q, want both messages", texts)
	}
}

func TestRemovingAFolderKeepsItsLedgerForWhenItIsAddedAgain(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	path := plainFolder(t)
	id := addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: folderSession(t, r, "worker", path).ID, PromptID: "p1", Prompt: "keep notes"})

	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+id+`"}`); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	if _, err := ResolvePathLedgerKey(context.Background(), r, path); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("a removed folder's ledger is not named: %v", err)
	}
	if again := addFolderProject(t, d, path); again != id {
		t.Fatalf("adding the folder again made project %s, want %s", again, id)
	}
	if got := ledgerKeyAt(t, r, path); got != key {
		t.Fatalf("ledger key after adding again = %q, want %s", got, key)
	}
	if texts := utteranceTexts(t, store, key); len(texts) != 1 || texts[0] != "keep notes" {
		t.Fatalf("ledger after adding again holds %q", texts)
	}
}

func TestALinkNeverLeavesAKeylessLedgerBehind(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	kept, other := plainFolder(t), plainFolder(t)
	keptID, otherID := addFolderProject(t, d, kept), addFolderProject(t, d, other)
	store := ledger.NewStore(r.WriteDB(), r.ReadDB())
	if err := store.Enable(ledgerKeyAt(t, r, kept), ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}

	refused := runDashboardAction(t, d, `{"action":"project-link","projectId":"`+keptID+`","survivor":"`+otherID+`"}`)
	if refused.Code != http.StatusConflict || apiErrorCode(t, refused) != "project_history" {
		t.Fatalf("linking away the project holding the ledger: %d %s", refused.Code, refused.Body)
	}
	if response := runDashboardAction(t, d, `{"action":"project-link","projectId":"`+otherID+`","survivor":"`+keptID+`"}`); response.Code != http.StatusOK {
		t.Fatalf("link: %d %s", response.Code, response.Body)
	}
	if err := d.refreshProjectLinks(); err != nil {
		t.Fatal(err)
	}
	targets, _, err := d.ledgerTargets(store, folderSession(t, r, "joined", other))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].key != registry.LedgerProjectKey(keptID) {
		t.Fatalf("a session in the joined folder writes to %+v, want the survivor's ledger", targets)
	}
}

func TestALinkFindsAKeylessLedgerUnderTheRemoteItsProjectGained(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := catalogTestRepo(t)
	gitRun(t, path, "remote", "add", "origin", "/srv/git/notes.git")
	addFolderProject(t, d, path)
	other := addFolderProject(t, d, plainFolder(t))
	if err := ledger.NewStore(r.WriteDB(), r.ReadDB()).Enable(ledgerKeyAt(t, r, path), ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	gitRun(t, path, "remote", "set-url", "origin", "https://github.com/example/notes.git")
	records, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	d.refreshProjectRepositories(context.Background(), records, time.Now())
	refused := runDashboardAction(t, d, `{"action":"project-link","projectId":"repo:github.com/example/notes","survivor":"`+other+`"}`)
	if refused.Code != http.StatusConflict || apiErrorCode(t, refused) != "project_history" {
		t.Fatalf("linking away a project whose ledger predates its remote: %d %s", refused.Code, refused.Body)
	}
}

func TestASessionInAKeylessProjectReadsItsLedgerOverMCP(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := plainFolder(t)
	id := addFolderProject(t, d, path)
	if err := ledger.NewStore(r.WriteDB(), r.ReadDB()).Enable(ledgerKeyAt(t, r, path), ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, folderSession(t, r, "worker", path)))
	if board, refused := callMCP(t, session, "agenthail_ledger_read", nil); refused || board["projectKey"] != registry.LedgerProjectKey(id) {
		t.Fatalf("board = %v", board)
	}
	write := map[string]any{"op": "work", "clientRequestId": "w1", "request": map[string]any{"action": "add", "text": "tidy the notes"}}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", write); refused {
		t.Fatalf("write: %v", out)
	}
	board, err := d.ledgerStore().Board(registry.LedgerProjectKey(id))
	if err != nil {
		t.Fatal(err)
	}
	if work := board.Ready(); len(work) != 1 {
		t.Fatalf("the MCP write did not land in the project's ledger: %+v", work)
	}
}

func TestAGrantAloneKeepsAKeylessProjectsLedger(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	path := catalogTestRepo(t)
	gitRun(t, path, "remote", "add", "origin", "/srv/git/notes.git")
	id := addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	folderSession(t, r, "worker", path)
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/scope", ledgerScopeRequest{ProjectKey: key, SessionID: "worker", Enabled: true}, nil); code != http.StatusOK {
		t.Fatalf("grant: %d %s", code, body)
	}

	gitRun(t, path, "remote", "set-url", "origin", "https://github.com/example/notes.git")
	records, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	d.refreshProjectRepositories(context.Background(), records, time.Now())
	if got := ledgerKeyAt(t, r, path); got != key {
		t.Fatalf("a granted ledger moved to %q when its folder gained a remote", got)
	}
	if response := runDashboardAction(t, d, `{"action":"project-remove","projectId":"`+id+`"}`); response.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", response.Code, response.Body)
	}
	if _, err := ResolvePathLedgerKey(context.Background(), r, path); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("the removed folder resolves without naming its kept ledger: %v", err)
	}
	if again := addFolderProject(t, d, path); again != id || ledgerKeyAt(t, r, path) != key {
		t.Fatalf("adding the folder again made project %s", again)
	}
	grants, err := d.ledgerStore().SessionGrants("worker")
	if err != nil || len(grants) != 1 || grants[0].ProjectKey != key {
		t.Fatalf("grants = %+v %v", grants, err)
	}
}
