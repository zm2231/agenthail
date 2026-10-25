package daemon

import (
	"net/http"
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestDashboardEntryAndItsImportsAreServed(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	page := serveDashboardRequest(handler, http.MethodGet, "/", "")
	if !strings.Contains(page.Body.String(), `<script type="module" src="/js/main.js"></script>`) || strings.Contains(page.Body.String(), "/app.js") {
		t.Fatal("the page must load the module entry")
	}
	if response := serveDashboardRequest(handler, http.MethodGet, "/app.js", ""); response.Code != http.StatusNotFound {
		t.Fatalf("obsolete script: status=%d", response.Code)
	}
	imports := regexp.MustCompile(`(?:from\s*|import\s*\()"(\.[^"\n]+\.js)"`)
	pending := []string{"/js/main.js"}
	seen := map[string]bool{}
	for len(pending) > 0 {
		url := pending[0]
		pending = pending[1:]
		if seen[url] {
			continue
		}
		seen[url] = true
		response := serveDashboardRequest(handler, http.MethodGet, url, "")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d type=%q cache=%q", url, response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"))
		}
		for _, match := range imports.FindAllStringSubmatch(response.Body.String(), -1) {
			pending = append(pending, path.Join(path.Dir(url), match[1]))
		}
	}
	if !seen["/js/tool-activity.js"] {
		t.Fatal("the import graph must include the deferred tool renderer")
	}
}
