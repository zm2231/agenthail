package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
		if err := os.MkdirAll(browserhost.StateDir(home), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared, browserhost.BrowsersDir(home)); err != nil {
			t.Fatal(err)
		}
	}
	_, file, _, _ := runtime.Caller(0)
	launch := browserhost.Launch{Node: testnode.Path(t), Script: filepath.Join(filepath.Dir(file), "..", "..", "browser-host", "host.mjs"), Version: "test", Env: os.Environ()}
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
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
