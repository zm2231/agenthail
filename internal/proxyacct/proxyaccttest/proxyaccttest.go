package proxyaccttest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// Proxy answers like CLIProxyAPI: healthz without a key, the management
// routes with Key. Writes change Files and are recorded in order. FailWrite
// "<name> <status> <body>" answers writes to one account with that refusal.
type Proxy struct {
	Server    *httptest.Server
	Attempts  atomic.Int32
	Files     atomic.Value
	Forced    atomic.Value
	FailWrite atomic.Value

	mu     sync.Mutex
	writes []string
}

func New(t *testing.T) *Proxy {
	proxy := &Proxy{}
	proxy.Files.Store(AuthFiles)
	proxy.Forced.Store("")
	proxy.FailWrite.Store("")
	proxy.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		route := map[string]string{"GET /v0/management/auth-files": "read", "PATCH /v0/management/auth-files/fields": "fields", "POST /v0/management/auth-files/release-sessions": "release"}[r.Method+" "+r.URL.Path]
		if route == "" {
			http.NotFound(w, r)
			return
		}
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
		if route == "read" {
			w.Write([]byte(proxy.Files.Load().(string)))
			return
		}
		proxy.write(w, r, route)
	}))
	t.Cleanup(proxy.Server.Close)
	return proxy
}

func (p *Proxy) write(w http.ResponseWriter, r *http.Request, route string) {
	body, _ := io.ReadAll(r.Body)
	var request map[string]any
	if json.Unmarshal(body, &request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid request body"}`))
		return
	}
	name, _ := request["name"].(string)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes = append(p.writes, route+" "+string(body))
	if failing, answer, _ := strings.Cut(p.FailWrite.Load().(string), " "); failing != "" && failing == name {
		code, body, _ := strings.Cut(answer, " ")
		status, _ := strconv.Atoi(code)
		w.WriteHeader(status)
		w.Write([]byte(body))
		return
	}
	var files map[string][]map[string]any
	json.Unmarshal([]byte(p.Files.Load().(string)), &files)
	for _, file := range files["files"] {
		if file["name"] != name {
			continue
		}
		if route == "fields" {
			file["quota_preference"] = request["quota_preference"]
			encoded, _ := json.Marshal(files)
			p.Files.Store(string(encoded))
		}
		w.Write([]byte(`{"status":"ok"}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"auth file not found"}`))
}

func (p *Proxy) Writes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.writes...)
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
