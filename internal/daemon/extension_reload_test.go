package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/poll"
)

const stampedPage = `<!doctype html><title>loading</title>
<script type="module">
  import { id, action } from '/_ah.js';
  document.title = id + ' ' + Math.random().toString(36).slice(2);
  action('loaded');
</script>`

type signedInBrowser struct {
	*httptest.Server
	page      string
	home      string
	listening atomic.Int32
}

func (b *signedInBrowser) loads() int {
	got, _ := os.ReadFile(filepath.Join(b.home, ".agenthail", "page-loads"))
	n, _ := strconv.Atoi(string(got))
	return n
}

// signIn serves the dashboard to the browser as the person, and /signin gives
// the browser the dashboard cookie, which it then sends to every port on
// 127.0.0.1, the page's too. The dashboard's event streams wait for the page's
// first load, so its replay of past changes always finds the page framed.
func signIn(t *testing.T, f *extensionFixture, agent *mcp.ClientSession) *signedInBrowser {
	t.Helper()
	target, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookie := signInWithLoginLink(t, f.server.Config.Handler)
	b := &signedInBrowser{page: f.pageURL(t), home: f.home}
	b.Server = httptest.NewServer(&httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		if r.In.URL.Path == "/api/v1/events" {
			ctx, cancel := context.WithTimeout(r.In.Context(), 30*time.Second)
			_ = poll.Until(ctx, 20*time.Millisecond, func() (bool, error) { return b.loads() > 0, nil })
			cancel()
			b.listening.Add(1)
		}
		r.SetURL(target)
		r.Out.Header.Set("Authorization", "Bearer secret")
		r.Out.AddCookie(cookie)
	}})
	t.Cleanup(func() {
		b.CloseClientConnections()
		b.Close()
	})
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: cookie.Name, Value: cookie.Value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		_, _ = w.Write([]byte("<!doctype html><title>signed in</title>"))
	}))
	t.Cleanup(signin.Close)
	f.d.dashboard.Load().listen = strings.TrimPrefix(b.URL, "http://")
	tab := browse(t, agent, map[string]any{"action": "open", "url": signin.URL})["tab"].(string)
	eventually(t, "the browser signs in", func() bool {
		return browse(t, agent, map[string]any{"action": "snapshot", "tab": tab})["title"] == "signed in"
	})
	return b
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
	agent := browserAgent(t, f)
	browser := signIn(t, f, agent)
	changeOwnPage(t, f, "green", "turn-0")

	tab := browse(t, agent, map[string]any{"action": "open", "url": browser.page + "/"})["tab"].(string)
	title := func() string {
		return browse(t, agent, map[string]any{"action": "snapshot", "tab": tab})["title"].(string)
	}
	eventually(t, "the page loads", func() bool { return strings.HasPrefix(title(), "fixture ") })
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
	agent := browserAgent(t, f)
	browser := signIn(t, f, agent)
	changeOwnPage(t, f, "green", "turn-0")

	browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/#extensions"})
	eventually(t, "the dashboard frames the page", func() bool { return browser.loads() > 0 })
	eventually(t, "the dashboard listens for changes", func() bool { return browser.listening.Load() > 0 })

	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads(); n != 1 {
		t.Fatalf("the framed page loaded %d times with nothing changed", n)
	}

	changeOtherExtension(t, f, "turn-1")
	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads(); n != 1 {
		t.Fatalf("a change to another extension reloaded the framed page: %d loads", n)
	}

	changeOwnPage(t, f, "blue", "turn-2")
	eventually(t, "a change to the framed page's extension reloads it", func() bool { return browser.loads() == 2 })
	time.Sleep(1500 * time.Millisecond)
	if n := browser.loads(); n != 2 {
		t.Fatalf("one change loaded the framed page %d times", n)
	}
}
