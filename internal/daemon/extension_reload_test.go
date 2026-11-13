package daemon

import (
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/extension"
)

const stampedPage = `<!doctype html><title>loading</title>
<script type="module">
  import { id } from '/x/_ah.js';
  document.title = id + ' ' + Math.random().toString(36).slice(2);
</script>`

func TestAPageOpenedOnItsOwnReloadsForItsOwnChangesOnly(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"ui.html": stampedPage})
	target, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var listening atomic.Int32
	signedIn := &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		if r.In.URL.Path == "/api/v1/events" {
			listening.Add(1)
		}
		r.SetURL(target)
		r.Out.Header.Set("Authorization", "Bearer secret")
	}}
	browser := httptest.NewServer(signedIn)
	t.Cleanup(browser.Close)

	if err := os.WriteFile(filepath.Join(f.workdir, "assets", "app.css"), []byte("body { color: green }"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "to", "turn-0")

	host := runTestBrowserHost(t, f.d)
	eventually(t, "the browser host answers", func() bool { return host.Status().State != browserhost.StateUnavailable })
	agent := mcpSession(t, mcpServer(t, f.d), issueMCPToken(t, f.d, f.fake.sessions["from"]))
	tab := browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/x/fixture/"})["tab"].(string)
	title := func() string {
		return browse(t, agent, map[string]any{"action": "snapshot", "tab": tab})["title"].(string)
	}
	eventually(t, "the page loads", func() bool { return strings.HasPrefix(title(), "fixture ") })
	eventually(t, "the page listens for changes", func() bool { return listening.Load() > 0 })
	first := title()

	time.Sleep(1500 * time.Millisecond)
	if again := title(); again != first {
		t.Fatalf("the page reloaded with nothing changed: %q then %q", first, again)
	}

	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes"}`)
	f.turn(t, "to", "turn-1")
	time.Sleep(1500 * time.Millisecond)
	if again := title(); again != first {
		t.Fatalf("a change to another extension reloaded the page: %q then %q", first, again)
	}

	if err := os.WriteFile(filepath.Join(f.workdir, "assets", "app.css"), []byte("body { color: blue }"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "to", "turn-2")
	eventually(t, "a change to the page's own extension reloads it", func() bool {
		now := title()
		return strings.HasPrefix(now, "fixture ") && now != first
	})
}
