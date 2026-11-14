package daemon

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

const fetchFixtureServer = `
export default function (ah: any) {
  ah.action('fetch', async (input: any) => {
    try {
      const response = await ah.fetch(input.mount ?? 'app', input.path, input.init ?? {});
      return { status: response.status, headers: response.headers, body: response.text() };
    } catch (error: any) {
      return { code: error.code, message: error.message };
    }
  });
}
`

type fetchOutcome struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
}

func TestAnExtensionFetchesOnlyFromItsMountsUpstreamAndNeverSeesTheKey(t *testing.T) {
	const key = "mount-key-4d1f9a"
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a request reached another origin carrying %q", r.Header.Get("Authorization"))
	}))
	t.Cleanup(elsewhere.Close)
	upstream, seen := startFetchUpstream(t, elsewhere.URL)

	manifest := fmt.Sprintf(`{"label": "Fixture", "mounts": [{"name": "app", "label": "App", "upstream": %q, "entry": "/"}]}`, upstream.URL)
	f := startExtensionFixtureWith(t, map[string]string{"extension.json": manifest, "server.ts": fetchFixtureServer})
	previous := extensionPortBase
	extensionPortBase = 0
	t.Cleanup(func() { extensionPortBase = previous })
	t.Cleanup(f.d.extensions.mounts.close)

	fetch := func(input map[string]any) fetchOutcome {
		t.Helper()
		status, body := f.action(t, "fetch", input)
		var outcome fetchOutcome
		if status != http.StatusOK || json.Unmarshal(body["result"], &outcome) != nil {
			t.Fatalf("fetch %v answered %d %s", input, status, body)
		}
		if strings.Contains(string(body["result"]), key) {
			t.Fatalf("fetch %v showed the extension the key: %s", input, body["result"])
		}
		return outcome
	}

	if got := fetch(map[string]any{"path": "/echo"}); got.Code != "not_found" {
		t.Fatalf("an unapproved mount was fetched: %+v", got)
	}
	if err := f.d.approveExtension("fixture"); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.setExtensionMountKey("fixture", "app", &proxyacct.KeyRef{File: keyFile}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}

	got := fetch(map[string]any{"path": "/echo?q=1", "init": map[string]any{"method": "POST", "body": map[string]any{"a": 1}}})
	request := seen.last(t)
	if request.Header.Get("Authorization") != "Bearer "+key || request.URL.RequestURI() != "/echo?q=1" || request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("the upstream saw %s %s with %v", request.Method, request.URL.RequestURI(), request.Header)
	}
	if got.Status != http.StatusUnauthorized || !strings.Contains(got.Body, "denied for Bearer "+extensionFetchRedacted) || got.Headers["x-echo"] != "Bearer "+extensionFetchRedacted {
		t.Fatalf("an error answer that echoed the key came back as %+v", got)
	}

	if got := fetch(map[string]any{"path": "/hop"}); got.Status != http.StatusUnauthorized || seen.last(t).URL.Path != "/echo" {
		t.Fatalf("a redirect within the upstream was not followed: %+v", got)
	}
	if got := fetch(map[string]any{"path": "/away"}); got.Code != "redirect_refused" {
		t.Fatalf("a redirect to another origin answered %+v", got)
	}

	for _, path := range []string{elsewhere.URL + "/echo", "//" + strings.TrimPrefix(elsewhere.URL, "http://") + "/echo", "echo", `/\evil`} {
		if got := fetch(map[string]any{"path": path}); got.Code != "invalid" {
			t.Fatalf("path %q answered %+v", path, got)
		}
	}
	if got := fetch(map[string]any{"path": "/gzip"}); got.Status != http.StatusUnauthorized || got.Body != "denied for Bearer "+extensionFetchRedacted {
		t.Fatalf("a compressed answer the daemon asked for came back as %+v", got)
	}
	if got := fetch(map[string]any{"path": "/brotli"}); got.Code != "encoded" {
		t.Fatalf("an answer in an encoding the daemon did not ask for came back as %+v", got)
	}
	for _, header := range []string{"Host", "authorization", "Cookie", "X-Forwarded-Host", "Accept-Encoding", "Range", "If-Range"} {
		if got := fetch(map[string]any{"path": "/echo", "init": map[string]any{"headers": map[string]string{header: "x"}}}); got.Code != "invalid" {
			t.Fatalf("header %s answered %+v", header, got)
		}
	}
	if got := fetch(map[string]any{"path": "/big"}); got.Code != "too_large" {
		t.Fatalf("an oversized answer came back as %+v", got)
	}

	response, _ := f.request(t, http.MethodPost, extensionFetchPath, "secret", map[string]string{"mount": "app", "path": "/echo"})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a dashboard token fetched through a mount: %d", response.StatusCode)
	}
}

func startFetchUpstream(t *testing.T, elsewhere string) (*httptest.Server, *upstreamSeen) {
	t.Helper()
	seen := &upstreamSeen{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		switch r.URL.Path {
		case "/echo":
			w.Header().Set("X-Echo", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "denied for "+r.Header.Get("Authorization"))
		case "/hop":
			http.Redirect(w, r, "/echo", http.StatusFound)
		case "/away":
			http.Redirect(w, r, elsewhere+"/echo", http.StatusFound)
		case "/gzip":
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				t.Errorf("the daemon did not ask for gzip: %q", r.Header.Get("Accept-Encoding"))
			}
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusUnauthorized)
			zipped := gzip.NewWriter(w)
			_, _ = io.WriteString(zipped, "denied for "+r.Header.Get("Authorization"))
			_ = zipped.Close()
		case "/brotli":
			w.Header().Set("Content-Encoding", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			_, _ = io.WriteString(w, r.Header.Get("Authorization"))
		case "/big":
			_, _ = io.WriteString(w, strings.Repeat("x", extensionFetchResponseLimit+1))
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}
