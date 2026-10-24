package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardServesStylesOnlyFromItsCSSDirectory(t *testing.T) {
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
	response := get(http.MethodGet, "/css/panes.css")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/css; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "[data-pane") {
		t.Fatalf("panes.css: status=%d type=%q cache=%q", response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
	}
	for _, path := range []string{"/css/../dashboard.go", "/css/%2e%2e/dashboard.go", "/css/", "/css/missing.css", "/css/../tokens.css", "/css/panes.css/"} {
		response := get(http.MethodGet, path)
		if response.Code == http.StatusOK || strings.Contains(response.Body.String(), "package daemon") {
			t.Errorf("%s: status=%d body=%.80q", path, response.Code, response.Body.String())
		}
	}
	if response := get(http.MethodPost, "/css/panes.css"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", response.Code)
	}
	if page := get(http.MethodGet, "/"); !strings.Contains(page.Body.String(), `<link rel="stylesheet" href="/css/panes.css" />`) {
		t.Fatal("the page does not load the pane styles")
	}
}
