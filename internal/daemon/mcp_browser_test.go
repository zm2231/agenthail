package daemon

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/testnode"
)

type browserFixture struct {
	d      *Daemon
	host   *browserhost.Manager
	server *httptest.Server
	page   string
	parent *mcp.ClientSession
	child  *mcp.ClientSession
	token  string
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	home, err := os.MkdirTemp("", "bh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	if shared := os.Getenv("AGENTHAIL_BROWSER_TEST_BROWSERS"); shared != "" {
		if err := os.MkdirAll(shared, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(browserhost.StateDir(home), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared, browserhost.BrowsersDir(home)); err != nil {
			t.Fatal(err)
		}
	}
	launch := browserhost.Launch{Node: testnode.Path(t), Script: filepath.Join(testnode.BrowserHost(t), "host.mjs"), Version: "test", Env: os.Environ()}
	d, _, _, parent, child := daemonFixture(t)
	manager := &browserhost.Manager{Home: home, Launch: func() (browserhost.Launch, error) { return launch, nil }}
	d.SetBrowserHost(manager)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/long" {
			fmt.Fprint(w, `<!doctype html><title>Long</title><ul>`)
			for i := range 600 {
				fmt.Fprintf(w, `<li><a href="#%d">"quoted" <item> & %d</a> <button>Buy %d</button></li>`, i, i, i)
			}
			fmt.Fprint(w, `</ul>`)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Counter</title><button onclick="out.textContent='pressed'">Press</button><p id="out">waiting</p>`)
	}))
	t.Cleanup(page.Close)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, parent)
	return &browserFixture{d: d, host: manager, server: server, page: page.URL, parent: mcpSession(t, server, token), child: mcpSession(t, server, issueMCPToken(t, d, child)), token: token}
}

func browse(t *testing.T, session *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	out, refused := callMCP(t, session, "agenthail_browser", args)
	if refused {
		t.Fatalf("agenthail_browser %v: %v", args, out)
	}
	return out
}

func tabIDs(out map[string]any) []string {
	var ids []string
	for _, entry := range out["tabs"].([]any) {
		ids = append(ids, entry.(map[string]any)["tab"].(string))
	}
	return ids
}

func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := poll.Until(ctx, 50*time.Millisecond, func() (bool, error) { return check(), nil }); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func TestAnAgentsTabOutlivesItsTurnStaysPrivateAndClosesWhenTheSessionIsArchived(t *testing.T) {
	f := newBrowserFixture(t)
	eventually(t, "the browser host answers", func() bool { return f.host.Status().State != browserhost.StateUnavailable })
	opened := browse(t, f.parent, map[string]any{"action": "open", "url": f.page})
	tab := opened["tab"].(string)
	if opened["title"] != "Counter" || !strings.Contains(opened["next"].(string), "snapshot") {
		t.Fatalf("open = %v", opened)
	}
	snapshot := browse(t, f.parent, map[string]any{"action": "snapshot", "tab": tab})["snapshot"].(string)
	ref := regexp.MustCompile(`button "Press" \[ref=(\w+)\]`).FindStringSubmatch(snapshot)
	if ref == nil {
		t.Fatalf("no button ref in %s", snapshot)
	}
	browse(t, f.parent, map[string]any{"action": "act", "tab": tab, "kind": "click", "ref": ref[1]})

	nextTurn := mcpSession(t, f.server, f.token)
	if snapshot := browse(t, nextTurn, map[string]any{"action": "snapshot", "tab": tab})["snapshot"].(string); !strings.Contains(snapshot, "pressed") {
		t.Fatalf("the tab lost its page between turns: %s", snapshot)
	}
	if ids := tabIDs(browse(t, f.child, map[string]any{"action": "list"})); len(ids) != 0 {
		t.Fatalf("another session lists %v", ids)
	}
	if code := refusalCode(t, f.child, "agenthail_browser", map[string]any{"action": "snapshot", "tab": tab}); code != "not_found" {
		t.Fatalf("another session's snapshot: code %q", code)
	}
	if status := f.host.Status(); !status.Running {
		t.Fatalf("status after open = %+v", status)
	}

	if _, err := f.d.doSessionArchive("session-archive", "from"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "archiving closes the session's tabs", func() bool {
		var listed map[string]any
		err := f.host.Client().Call(t.Context(), "tabs.list", map[string]any{"owner": "from"}, &listed)
		return err == nil && len(tabIDs(listed)) == 0
	})
}

func TestAFinishedLedgerCheckerLeavesNoBrowserTabsOpen(t *testing.T) {
	f := newBrowserFixture(t)
	eventually(t, "the browser host answers", func() bool { return f.host.Status().State != browserhost.StateUnavailable })
	browse(t, f.child, map[string]any{"action": "open", "url": f.page})
	f.d.endChecker(t.Context(), "to", time.Now())
	eventually(t, "ending the checker closes its tabs", func() bool {
		var listed map[string]any
		err := f.host.Client().Call(t.Context(), "tabs.list", map[string]any{"owner": "to"}, &listed)
		return err == nil && len(tabIDs(listed)) == 0
	})
}

func TestAfterTheBrowserHostRestartsASessionsTabsAreGoneAndOpenWorksAgain(t *testing.T) {
	f := newBrowserFixture(t)
	eventually(t, "the browser host answers", func() bool { return f.host.Status().State != browserhost.StateUnavailable })
	tab := browse(t, f.parent, map[string]any{"action": "open", "url": f.page})["tab"].(string)
	hello, err := f.host.Client().Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(hello.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a new browser host answers", func() bool {
		next, err := f.host.Client().Hello(t.Context())
		return err == nil && next.PID != hello.PID
	})
	if ids := tabIDs(browse(t, f.parent, map[string]any{"action": "list"})); len(ids) != 0 {
		t.Fatalf("tabs after the restart: %v", ids)
	}
	if code := refusalCode(t, f.parent, "agenthail_browser", map[string]any{"action": "snapshot", "tab": tab}); code != "not_found" {
		t.Fatalf("the old tab id: code %q", code)
	}
	reopened := browse(t, f.parent, map[string]any{"action": "open", "url": f.page})
	if reopened["tab"] == tab || reopened["title"] != "Counter" {
		t.Fatalf("open after the restart = %v", reopened)
	}
}

func TestASnapshotOfALongPageReturnsAnExcerptAndTheFileThatHoldsAllOfIt(t *testing.T) {
	f := newBrowserFixture(t)
	eventually(t, "the browser host answers", func() bool { return f.host.Status().State != browserhost.StateUnavailable })
	tab := browse(t, f.parent, map[string]any{"action": "open", "url": f.page + "/long"})["tab"].(string)
	taken := browse(t, f.parent, map[string]any{"action": "snapshot", "tab": tab})
	file, _ := taken["file"].(string)
	whole, err := os.ReadFile(file)
	if err != nil || taken["truncated"] != true || !strings.Contains(taken["full"].(string), file) {
		t.Fatalf("snapshot = %v, file err %v", taken, err)
	}
	if len(whole) <= mcpOutputLimit || !strings.Contains(string(whole), `button "Buy 599"`) || !strings.HasPrefix(string(whole), taken["snapshot"].(string)) {
		t.Fatalf("the file holds %d bytes; want the whole tree, beginning with the excerpt", len(whole))
	}

	shot := browse(t, f.parent, map[string]any{"action": "screenshot", "tab": tab, "fullPage": true})
	if png, err := os.ReadFile(shot["file"].(string)); err != nil || !bytes.HasPrefix(png, []byte("\x89PNG")) || shot["height"].(float64) <= 720 {
		t.Fatalf("screenshot = %v, err %v", shot, err)
	}
	if code := refusalCode(t, f.child, "agenthail_browser", map[string]any{"action": "screenshot", "tab": tab}); code != "not_found" {
		t.Fatalf("another session's screenshot: code %q", code)
	}
}

func TestTextAndLinksFitTheResultAndAScreenshotIsSavedOnlyInsideTheSessionsFolders(t *testing.T) {
	f := newBrowserFixture(t)
	eventually(t, "the browser host answers", func() bool { return f.host.Status().State != browserhost.StateUnavailable })
	real, outside := t.TempDir(), t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	if err := os.Symlink(real, work); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(real); err != nil {
		t.Fatal(err)
	} else {
		real = resolved
	}
	session, err := f.d.Registry.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	session.Cwd = work
	if err := f.d.Registry.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	tab := browse(t, f.parent, map[string]any{"action": "open", "url": f.page + "/long"})["tab"].(string)
	text := browse(t, f.parent, map[string]any{"action": "text", "tab": tab})
	if whole, err := os.ReadFile(text["file"].(string)); err != nil || text["truncated"] != true || !strings.Contains(string(whole), "Buy 599") {
		t.Fatalf("text = %v, file err %v", text["full"], err)
	}
	if code := refusalCode(t, f.parent, "agenthail_browser", map[string]any{"action": "snapshot", "tab": tab, "depth": 0}); code != "invalid" {
		t.Fatalf("snapshot depth 0: code %q", code)
	}
	shallow := browse(t, f.parent, map[string]any{"action": "snapshot", "tab": tab, "depth": 1})
	if tree, err := os.ReadFile(shallow["file"].(string)); err != nil || shallow["lines"] != float64(601) || strings.Contains(string(tree), "link ") {
		t.Fatalf("snapshot depth 1: %v lines, want the list and its 600 items without their links (err %v)", shallow["lines"], err)
	}
	links := browse(t, f.parent, map[string]any{"action": "links", "tab": tab})
	if !strings.HasPrefix(links["links"].(string), `["quoted" & 0](`+f.page+`/long#0)`) {
		t.Fatalf("links begin %.120q", links["links"])
	}

	saved := filepath.Join(work, "page.png")
	shot := browse(t, f.parent, map[string]any{"action": "screenshot", "tab": tab, "path": saved})
	if png, err := os.ReadFile(saved); err != nil || shot["path"] != filepath.Join(real, "page.png") || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("screenshot = %v, err %v", shot, err)
	}
	if err := os.Symlink(outside, filepath.Join(work, "out")); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		saved:                                  "exists",
		filepath.Join(outside, "page.png"):     "out_of_scope",
		filepath.Join(work, "out", "page.png"): "out_of_scope",
		filepath.Join(work, "none", "a.png"):   "invalid",
		"page.png":                             "invalid",
		filepath.Join(work, "page.jpg"):        "invalid",
	} {
		if code := refusalCode(t, f.parent, "agenthail_browser", map[string]any{"action": "screenshot", "tab": tab, "path": path}); code != want {
			t.Errorf("screenshot to %s: code %q, want %q", path, code, want)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a screenshot was written outside the session's folders: %v", entries)
	}
}
