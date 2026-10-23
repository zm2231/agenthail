package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sessionFilesBody struct {
	Files     []sessionFile `json:"files"`
	Truncated bool          `json:"truncated"`
}

func getSessionFiles(t *testing.T, handler http.Handler, query string) (int, sessionFilesBody, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-files?"+query, nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body sessionFilesBody
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	return response.Code, body, response.Body.String()
}

func writeTree(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, name := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func filePaths(files []sessionFile) string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
		if file.Dir {
			paths[i] += "/"
		}
	}
	return strings.Join(paths, "|")
}

func TestSessionFilesFollowTheCheckoutsIgnoreRules(t *testing.T) {
	d, r, _, session, _ := daemonFixture(t)
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTree(t, repo, ".gitignore", "internal/daemon/dashboard.go", "internal/daemon/dashboard_test.go", "README.md", "untracked.txt", "build/out.bin", "node_modules/pkg/index.js")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore", "internal", "README.md")
	session.Cwd = filepath.Join(repo, "internal")
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})

	code, body, raw := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)+"&q=dashgo")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if got := filePaths(body.Files); got != "daemon/dashboard.go|daemon/dashboard_test.go" {
		t.Fatalf("files = %q, want the cwd's matches ranked by fzf, relative to cwd", got)
	}

	session.Cwd = repo
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	_, body, _ = getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID))
	got := filePaths(body.Files)
	for _, want := range []string{"README.md", "untracked.txt", "internal/", "internal/daemon/"} {
		if !strings.Contains("|"+got+"|", "|"+want+"|") {
			t.Fatalf("files = %q, want %q (tracked, untracked kept and implied directories)", got, want)
		}
	}
	for _, ignored := range []string{"build", "node_modules", ".git/"} {
		if strings.Contains(got, ignored) {
			t.Fatalf("files = %q list %q, which the checkout ignores", got, ignored)
		}
	}
	if strings.Index(got, "README.md") > strings.Index(got, "internal/daemon/dashboard.go") {
		t.Fatalf("files = %q, want shallow paths first without a query", got)
	}
	if body.Truncated {
		t.Fatal("a small checkout is not truncated")
	}
}

func TestSessionFilesWalkAPlainDirectoryWithoutLeavingIt(t *testing.T) {
	d, r, _, session, _ := daemonFixture(t)
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, "notes/plan.md", ".hidden/secret.txt", "node_modules/x.js")
	writeTree(t, outside, "elsewhere/private.txt")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	session.Cwd = dir
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	code, body, raw := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	got := filePaths(body.Files)
	if !strings.Contains(got, "notes/plan.md") || strings.Contains(got, "secret") || strings.Contains(got, "node_modules") || strings.Contains(got, "private") {
		t.Fatalf("files = %q, want visible files only and nothing through the symlink", got)
	}
}

func TestSessionFilesLimitsAndRefusals(t *testing.T) {
	d, r, _, session, _ := daemonFixture(t)
	dir := t.TempDir()
	for i := 0; i < 300; i++ {
		writeTree(t, dir, filepath.ToSlash(filepath.Join("many", strings.Repeat("f", 1+i%5)+string(rune('a'+i%26))+time.Duration(i).String()+".txt")))
	}
	session.Cwd = dir
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	if _, body, _ := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)); len(body.Files) != sessionFilesDefaultLimit {
		t.Fatalf("default limit returned %d", len(body.Files))
	}
	if _, body, _ := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)+"&limit=1000"); len(body.Files) != sessionFilesMaxLimit {
		t.Fatalf("limit is capped at %d, returned %d", sessionFilesMaxLimit, len(body.Files))
	}
	if code, _, raw := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)+"&limit=0"); code != http.StatusBadRequest || !strings.Contains(raw, "invalid_limit") {
		t.Fatalf("limit=0: %d %s", code, raw)
	}
	if code, _, raw := getSessionFiles(t, handler, "id=missing"); code != http.StatusNotFound || !strings.Contains(raw, "not_found") {
		t.Fatalf("missing session: %d %s", code, raw)
	}
	session.Cwd = ""
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)); code != http.StatusNotFound || !strings.Contains(raw, "no_cwd") {
		t.Fatalf("no cwd: %d %s", code, raw)
	}
	session.Cwd = filepath.Join(dir, "gone")
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := getSessionFiles(t, handler, "id="+url.QueryEscape(session.ID)); code != http.StatusNotFound || !strings.Contains(raw, "no_cwd") {
		t.Fatalf("missing cwd: %d %s", code, raw)
	}
}

func TestSessionFilesWalkStopsAtTheEntryCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		writeTree(t, dir, filepath.Join("d", strings.Repeat("x", i+1)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	if list := walkSessionFiles(ctx, dir); !list.truncated || len(list.files) != 0 {
		t.Fatalf("an expired walk is truncated and empty: %+v", list)
	}
}

func TestSessionFilesForARemoteSessionAreListedOnItsHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	session, err := remote.reg.Session("remote-a")
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, session.Cwd, "remote-only.go")
	read := local.request(t, http.MethodGet, "/api/v1/session-files?id="+url.QueryEscape(record.Name+"::remote-a")+"&q=remoteonly", nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"path":"remote-only.go"`) {
		t.Fatalf("remote files status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestSessionFilesNoticeAChangedCheckoutAtOnce(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	writeTree(t, repo, "a.go")
	runGit(t, repo, "add", "a.go")
	lists := sessionFileLists{}
	now := time.Now()
	if got := filePaths(lists.get(context.Background(), repo, now).files); got != "a.go" {
		t.Fatalf("files = %q", got)
	}
	writeTree(t, repo, "b.go")
	if got := filePaths(lists.get(context.Background(), repo, now.Add(time.Second)).files); got != "a.go" {
		t.Fatalf("an unchanged index serves the cached list, got %q", got)
	}
	time.Sleep(10 * time.Millisecond)
	runGit(t, repo, "add", "b.go")
	if got := filePaths(lists.get(context.Background(), repo, now.Add(2*time.Second)).files); got != "a.go|b.go" {
		t.Fatalf("a changed index lists again, got %q", got)
	}
	writeTree(t, repo, "c.go")
	if got := filePaths(lists.get(context.Background(), repo, now.Add(sessionFilesFresh+3*time.Second)).files); !strings.Contains(got, "c.go") {
		t.Fatalf("an old list is rebuilt, got %q", got)
	}
}
