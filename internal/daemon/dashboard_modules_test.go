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
	for _, path := range []string{"/js/ui.js", "/js/ui-contrast.js"} {
		response := get(http.MethodGet, path)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "export ") {
			t.Fatalf("%s: status=%d type=%q cache=%q", path, response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
		}
	}
	for _, path := range []string{"/js/../dashboard.go", "/js/%2e%2e/dashboard.go", "/js/", "/js/missing.js", "/js/../app.js", "/js/ui.js/"} {
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
}
