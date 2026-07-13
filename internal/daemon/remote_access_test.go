package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteURLUsesTailscaleHTTPS(t *testing.T) {
	value, err := remoteURL("agent.tailnet.ts.net", "http://127.0.0.1:7412/?token=secret", 7412)
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://agent.tailnet.ts.net:7412/?token=secret#overview" {
		t.Fatalf("URL=%q", value)
	}
}

func TestRemoteServeUsesHTTPSReadsAdvertisedRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailscale")
	body := "#!/bin/sh\nprintf '%s\\n' 'https://agent.tailnet.ts.net:7412 (tailnet only)'\n"
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	ready, err := remoteServeUsesHTTPS(path, "agent.tailnet.ts.net", 7412)
	if err != nil || !ready {
		t.Fatalf("ready=%t err=%v", ready, err)
	}
}

func TestDashboardBootstrapStripsTokenFromRedirect(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?token=secret", nil))
	if response.Code != 302 || response.Header().Get("Location") != "/" || strings.Contains(response.Header().Get("Location"), "token") {
		t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}
