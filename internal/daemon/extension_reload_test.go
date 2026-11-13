package daemon

import (
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/extension"
)

const stampedPage = `<!doctype html><title>loading</title>
<script type="module">
  import { id } from '/x/_ah.js';
  document.title = id + ' ' + Math.random().toString(36).slice(2);
  fetch('/x/' + id + '/loaded', { cache: 'no-store' });
</script>`

type signedInProxy struct {
	*httptest.Server
	listening atomic.Int32
	loads     atomic.Int32
	loaded    chan struct{}
	once      sync.Once
}

// signIn serves the daemon to the browser as the person. Event streams wait
// for the page's first load, so the dashboard's replay of past changes always
// finds the page already framed.
func signIn(t *testing.T, f *extensionFixture) *signedInProxy {
	t.Helper()
	target, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookie := signInWithLoginLink(t, f.server.Config.Handler)
	p := &signedInProxy{loaded: make(chan struct{})}
	p.Server = httptest.NewServer(&httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		switch r.In.URL.Path {
		case "/api/v1/events":
			select {
			case <-p.loaded:
			case <-time.After(30 * time.Second):
			}
			p.listening.Add(1)
		case "/x/fixture/loaded":
			p.loads.Add(1)
			p.once.Do(func() { close(p.loaded) })
		}
		r.SetURL(target)
		r.Out.Header.Set("Authorization", "Bearer secret")
		r.Out.AddCookie(cookie)
	}})
	t.Cleanup(p.Close)
	return p
}

func changeOwnPage(t *testing.T, f *extensionFixture, color, turn string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.workdir, "assets", "app.css"), []byte("body { color: "+color+" }"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "to", turn)
}

func changeOtherExtension(t *testing.T, f *extensionFixture, turn string) {
	t.Helper()
	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes"}`)
	f.turn(t, "to", turn)
}

func browserAgent(t *testing.T, f *extensionFixture) *mcp.ClientSession {
	t.Helper()
	host := runTestBrowserHost(t, f.d)
	eventually(t, "the browser host answers", func() bool { return host.Status().State != browserhost.StateUnavailable })
	return mcpSession(t, mcpServer(t, f.d), issueMCPToken(t, f.d, f.fake.sessions["from"]))
}

func TestAPageOpenedOnItsOwnReloadsForItsOwnChangesOnly(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"ui.html": stampedPage})
	browser := signIn(t, f)
	changeOwnPage(t, f, "green", "turn-0")

	agent := browserAgent(t, f)
	tab := browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/x/fixture/"})["tab"].(string)
	title := func() string {
		return browse(t, agent, map[string]any{"action": "snapshot", "tab": tab})["title"].(string)
	}
	eventually(t, "the page loads", func() bool { return strings.HasPrefix(title(), "fixture ") })
	eventually(t, "the page listens for changes", func() bool { return browser.listening.Load() > 0 })
	first := title()

	time.Sleep(1500 * time.Millisecond)
	if again := title(); again != first {
		t.Fatalf("the page reloaded with nothing changed: %q then %q", first, again)
	}

	changeOtherExtension(t, f, "turn-1")
	time.Sleep(1500 * time.Millisecond)
	if again := title(); again != first {
		t.Fatalf("a change to another extension reloaded the page: %q then %q", first, again)
	}

	changeOwnPage(t, f, "blue", "turn-2")
	eventually(t, "a change to the page's own extension reloads it", func() bool {
		now := title()
		return strings.HasPrefix(now, "fixture ") && now != first
	})
}

func TestTheDashboardReloadsTheFramedPageForItsOwnChangesOnly(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"ui.html": stampedPage})
	browser := signIn(t, f)
	changeOwnPage(t, f, "green", "turn-0")

	agent := browserAgent(t, f)
	browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/#extensions"})
	eventually(t, "the dashboard frames the page", func() bool { return browser.loads.Load() > 0 })
	eventually(t, "the dashboard listens for changes", func() bool { return browser.listening.Load() > 0 })

	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads.Load(); n != 1 {
		t.Fatalf("the framed page loaded %d times with nothing changed", n)
	}

	changeOtherExtension(t, f, "turn-1")
	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads.Load(); n != 1 {
		t.Fatalf("a change to another extension reloaded the framed page: %d loads", n)
	}

	changeOwnPage(t, f, "blue", "turn-2")
	eventually(t, "a change to the framed page's extension reloads it", func() bool { return browser.loads.Load() == 2 })
	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads.Load(); n != 2 {
		t.Fatalf("one change loaded the framed page %d times", n)
	}
}
