package proxyaccttest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const Key = "fixture-management-key"

const AuthFiles = `{"observed_at":"2026-10-26T12:00:00Z","files":[
 {"name":"claude-dev.json","provider":"claude","email":"dev@example.com","status":"active","quota_preference":10,
  "quota_ceiling":{"5h":60,"7d":60,"7d_oi":60},
  "quota":{"observed_at":"2026-10-26T11:58:00Z","signals":{"Anthropic-Ratelimit-Unified-5h-Utilization":"0.18","Anthropic-Ratelimit-Unified-5h-Reset":"1793016000","Anthropic-Ratelimit-Unified-7d-Utilization":"0.41","Anthropic-Ratelimit-Unified-7d-Reset":"1793400000"}},
  "model_quotas":{"claude-opus":{"observed_at":"2026-10-26T11:50:00Z","signals":{"Anthropic-Ratelimit-Unified-7d_oi-Utilization":"0.7","Anthropic-Ratelimit-Unified-7d_oi-Reset":"1793400000","Anthropic-Ratelimit-Unified-5h-Utilization":"0.10"}}}},
 {"name":"claude-team.json","provider":"claude","email":"team@example.com","status":"active","quota_preference":0,
  "cooldowns":[{"scope":"credential","reason":"quota","retry_at":"2099-01-01T00:00:00Z"}]},
 {"name":"claude-old.json","type":"claude","label":"old","disabled":true},
 {"name":"codex-dev.json","provider":"codex","email":"dev@example.com","status":"active",
  "model_quotas":{"gpt":{"observed_at":"2026-10-26T11:00:00Z","signals":{"x-codex-primary-used-percent":"12","x-codex-primary-window-minutes":"300","x-codex-primary-reset-at":"1793016000","x-codex-secondary-used-percent":"30","x-codex-secondary-window-minutes":"10080"}}}},
 {"name":"meta.json","provider":"meta"}
]}`

// Proxy answers like CLIProxyAPI: healthz without a key, auth-files with Key.
type Proxy struct {
	Server   *httptest.Server
	Attempts atomic.Int32
	Files    atomic.Value
	Forced   atomic.Value
}

func New(t *testing.T) *Proxy {
	proxy := &Proxy{}
	proxy.Files.Store(AuthFiles)
	proxy.Forced.Store("")
	proxy.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"status":"ok"}`))
		case "/v0/management/auth-files":
			proxy.Attempts.Add(1)
			if forced := proxy.Forced.Load().(string); forced != "" {
				code, body, _ := strings.Cut(forced, " ")
				w.WriteHeader(map[string]int{"401": 401, "403": 403, "404": 404}[code])
				w.Write([]byte(body))
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+Key {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"invalid management key"}`))
				return
			}
			w.Write([]byte(proxy.Files.Load().(string)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(proxy.Server.Close)
	return proxy
}

func KeyFile(t *testing.T, dir, key string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "proxy.key")
	if err := os.WriteFile(path, []byte(key+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
