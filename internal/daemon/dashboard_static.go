package daemon

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dashboard/index.html
var dashboardHTML []byte

//go:embed dashboard/tokens.css
var dashboardCSS []byte

//go:embed dashboard/js/*.js dashboard/js/*.webmanifest
var dashboardModules embed.FS

var dashboardModuleTypes = map[string]string{
	".js":          "application/javascript; charset=utf-8",
	".webmanifest": "application/manifest+json",
}

//go:embed dashboard/logo.png
var dashboardLogo []byte

//go:embed dashboard/favicon.png
var dashboardFavicon []byte

func (dashboard *dashboardServer) asset(contentType string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func dashboardModuleHandler() http.Handler {
	modules, err := fs.Sub(dashboardModules, "dashboard/js")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/js/", http.FileServerFS(modules))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		contentType, ok := dashboardModuleTypes[path.Ext(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

func (dashboard *dashboardServer) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, http.StatusNotFound, "unknown_route", "This Agenthail has no "+r.URL.Path+"; it runs an older version.")
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if secret := r.URL.Query().Get("login"); secret != "" {
		dashboard.login(w, r, secret)
		return
	}
	if !dashboard.authorized(r) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "sign in with a link from 'agenthail dashboard'")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(dashboardHTML)
}
