package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardServesModulesOnlyFromItsJSDirectory(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	get := func(method, path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/", nil)
		request.URL.Path = path
		addBrowserCookie(handler, request)
		handler.ServeHTTP(response, request)
		return response
	}
	for _, path := range []string{"/js/ui.js", "/js/ui-contrast.js", "/js/tool-activity.js", "/js/usage.js", "/js/usage-fit.js", "/js/usage-proxy.js"} {
		response := get(http.MethodGet, path)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "export ") {
			t.Fatalf("%s: status=%d type=%q cache=%q", path, response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
		}
	}
	if response := get(http.MethodGet, "/js/manifest.webmanifest"); response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/manifest+json" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"display": "standalone"`) {
		t.Fatalf("manifest: status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	if response := get(http.MethodGet, "/js/push-worker.js"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `addEventListener("push"`) {
		t.Fatalf("push worker: status=%d", response.Code)
	}
	for _, path := range []string{"/js/package.json", "/js/ui.js.map", "/js/manifest.json", "/js/../dashboard.go", "/js/%2e%2e/dashboard.go", "/js/", "/js/missing.js", "/js/../app.js", "/js/ui.js/"} {
		response := get(http.MethodGet, path)
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "package daemon") {
			t.Errorf("%s: status=%d body=%.80q", path, response.Code, response.Body.String())
		}
	}
	if response := get(http.MethodPost, "/js/ui.js"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", response.Code)
	}
	page := get(http.MethodGet, "/")
	if !strings.Contains(page.Body.String(), `<script type="module" src="/js/ui.js"></script>`) {
		t.Fatal("the page does not load the appearance module")
	}
	if !strings.Contains(page.Body.String(), `<link rel="manifest" href="/js/manifest.webmanifest" />`) {
		t.Fatal("the page does not name its manifest")
	}
}
