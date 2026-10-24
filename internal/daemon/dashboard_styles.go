package daemon

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dashboard/css/*.css
var dashboardStyles embed.FS

func dashboardStylesHandler() http.Handler {
	styles, err := fs.Sub(dashboardStyles, "dashboard/css")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/css/", http.FileServerFS(styles))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !strings.HasSuffix(r.URL.Path, ".css") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}
