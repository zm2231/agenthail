package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestSessionFilesStopAtTheEntryCap(t *testing.T) {
	saved := sessionFilesMaxEntries
	sessionFilesMaxEntries = 5
	t.Cleanup(func() { sessionFilesMaxEntries = saved })
	plain := t.TempDir()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	for i := 0; i < 8; i++ {
		name := filepath.Join("d", strings.Repeat("x", i+1))
		writeTree(t, plain, name)
		writeTree(t, repo, name)
	}
	if list := walkSessionFiles(context.Background(), plain); !list.truncated || len(list.files) != 5 {
		t.Fatalf("walk: truncated=%v entries=%d, want the cap", list.truncated, len(list.files))
	}
	list, ok := listGitFiles(context.Background(), repo)
	if !ok || !list.truncated || len(list.files) != 5 {
		t.Fatalf("git: ok=%v truncated=%v entries=%d, want the cap", ok, list != nil && list.truncated, len(list.files))
	}
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	if list := walkSessionFiles(ctx, plain); !list.truncated || len(list.files) != 0 {
		t.Fatalf("an expired walk is truncated and empty: %+v", list)
	}
}

func TestSessionFilesRankWithFzfsPathScoring(t *testing.T) {
	files := []sessionFile{{Path: "xdash.go"}, {Path: "a/dashboard.go"}, {Path: "docs/plan.md"}}
	if got := filePaths(matchSessionFiles(files, "dash", 10)); got != "a/dashboard.go|xdash.go" {
		t.Fatalf("ranked %q, want the match at a path boundary first", got)
	}
}

func TestSessionFilesBoundTheirCacheAndShareABuild(t *testing.T) {
	saved := buildFileList
	t.Cleanup(func() { buildFileList = saved })
	var mu sync.Mutex
	builds := map[string]int{}
	release := make(chan struct{})
	buildFileList = func(_ context.Context, cwd string, now time.Time) *sessionFileList {
		mu.Lock()
		builds[cwd]++
		mu.Unlock()
		<-release
		return &sessionFileList{files: []sessionFile{{Path: cwd}}, built: now}
	}
	lists := sessionFileLists{}
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists.get(context.Background(), "/same", now)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if builds["/same"] != 1 {
		t.Fatalf("concurrent cold requests built %d listings, want 1", builds["/same"])
	}
	for i := 0; i < 3*sessionFilesCachedLists; i++ {
		lists.get(context.Background(), "/cwd"+strconv.Itoa(i), now)
	}
	if len(lists.lists) != sessionFilesCachedLists || len(lists.recent) != sessionFilesCachedLists {
		t.Fatalf("cache holds %d lists (%d recent), want %d", len(lists.lists), len(lists.recent), sessionFilesCachedLists)
	}
}

func TestSessionFilesACancelledRequestDoesNotCutASharedListingShort(t *testing.T) {
	saved := buildFileList
	t.Cleanup(func() { buildFileList = saved })
	started := make(chan struct{})
	release := make(chan struct{})
	buildFileList = func(ctx context.Context, cwd string, now time.Time) *sessionFileList {
		close(started)
		<-release
		if ctx.Err() != nil {
			return &sessionFileList{truncated: true, built: now}
		}
		return &sessionFileList{files: []sessionFile{{Path: "kept.go"}}, built: now}
	}
	lists := sessionFileLists{}
	now := time.Now()
	owner, cancel := context.WithCancel(context.Background())
	ownerDone := make(chan *sessionFileList)
	go func() { ownerDone <- lists.get(owner, "/same", now) }()
	<-started
	waiterDone := make(chan *sessionFileList)
	go func() { waiterDone <- lists.get(context.Background(), "/same", now) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(release)
	<-ownerDone
	if got := <-waiterDone; got.truncated || filePaths(got.files) != "kept.go" {
		t.Fatalf("the waiter got %+v after the first request went away", got)
	}
	if cached := lists.lists["/same"]; cached == nil || cached.truncated {
		t.Fatalf("cached %+v", cached)
	}
}

func TestSessionFilesAStalledGitCannotHoldTheRequest(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	writeTree(t, dir, "a.txt")
	start := time.Now()
	list := (&sessionFileLists{}).get(context.Background(), dir, start)
	if elapsed := time.Since(start); elapsed > sessionFilesListDeadline+time.Second {
		t.Fatalf("a stalled git held the listing for %v", elapsed)
	}
	if !list.truncated {
		t.Fatalf("a listing cut short by the deadline is truncated: %+v", list)
	}
}

func TestSessionFilesInANestedRepositoryStayInsideIt(t *testing.T) {
	outer := t.TempDir()
	runGit(t, outer, "init", "-q")
	writeTree(t, outer, "outer.txt", "inner/child.txt")
	inner := filepath.Join(outer, "inner")
	runGit(t, inner, "init", "-q")
	list, ok := listGitFiles(context.Background(), inner)
	if !ok || filePaths(list.files) != "child.txt" {
		t.Fatalf("nested repository listed %q", filePaths(list.files))
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
