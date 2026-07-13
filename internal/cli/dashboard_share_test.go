package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

func TestTailscaleExecutableResolvesSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "tailscale-real")
	if err := os.WriteFile(realPath, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "tailscale")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	path, err := tailscaleExecutable(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.EvalSymlinks(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if path != wantPath {
		t.Fatalf("path=%q, want %q", path, wantPath)
	}
}

func TestReadTailscaleNodeRequiresOnlineMagicDNS(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"agent.tailnet.ts.net.","Online":true}}`), nil
	}
	status, err := readTailscaleNode(run)
	if err != nil {
		t.Fatal(err)
	}
	if status.Self.DNSName != "agent.tailnet.ts.net" {
		t.Fatalf("DNS name=%q", status.Self.DNSName)
	}
	missing := func(args ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Stopped","Self":{}}`), nil
	}
	if _, err := readTailscaleNode(missing); err == nil {
		t.Fatal("offline node accepted")
	}
}

func TestConfigureDashboardShareIsIdempotent(t *testing.T) {
	configured := false
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if reflect.DeepEqual(args, []string{"serve", "status", "--json"}) {
			if !configured {
				return []byte(`{}`), nil
			}
			return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}}}`), nil
		}
		if reflect.DeepEqual(args, []string{"serve", "status"}) {
			if configured {
				return []byte("https://agent.tailnet.ts.net:7412\n"), nil
			}
			return nil, nil
		}
		configured = true
		return []byte("ok"), nil
	}
	if err := configureDashboardShare(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err != nil {
		t.Fatal(err)
	}
	want := []string{"serve", "--bg", "--yes", "--https=7412", "http://127.0.0.1:7412"}
	if !reflect.DeepEqual(calls[2], want) {
		t.Fatalf("configure call=%v, want %v", calls[2], want)
	}
	calls = nil
	if err := configureDashboardShare(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("idempotent calls=%v", calls)
	}
}

func TestConfigureDashboardShareMigratesOwnedHTTPRoute(t *testing.T) {
	httpsReady := false
	var calls [][]string
	run := func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case reflect.DeepEqual(args, []string{"serve", "status", "--json"}):
			return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}}}`), nil
		case reflect.DeepEqual(args, []string{"serve", "status"}):
			if httpsReady {
				return []byte("https://agent.tailnet.ts.net:7412\n"), nil
			}
			return []byte("http://agent.tailnet.ts.net:7412\n"), nil
		case len(args) > 3 && args[3] == "--https=7412":
			httpsReady = true
		}
		return []byte("ok"), nil
	}
	if err := configureDashboardShare(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err != nil {
		t.Fatal(err)
	}
	wantOff := []string{"serve", "--http=7412", "off"}
	wantHTTPS := []string{"serve", "--bg", "--yes", "--https=7412", "http://127.0.0.1:7412"}
	foundOff, foundHTTPS := false, false
	for _, call := range calls {
		foundOff = foundOff || reflect.DeepEqual(call, wantOff)
		foundHTTPS = foundHTTPS || reflect.DeepEqual(call, wantHTTPS)
	}
	if !foundOff || !foundHTTPS {
		t.Fatalf("migration calls=%v", calls)
	}
}

func TestConfigureDashboardShareRejectsOccupiedPort(t *testing.T) {
	status := tailscaleServeStatus{TCP: map[string]struct {
		HTTP bool `json:"HTTP"`
	}{"7412": {HTTP: true}}, Web: map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	}{"agent.tailnet.ts.net:7412": {Handlers: map[string]struct {
		Proxy string `json:"Proxy"`
	}{"/": {Proxy: "http://127.0.0.1:9000"}}}}}
	data, _ := json.Marshal(status)
	run := func(args ...string) ([]byte, error) { return data, nil }
	if err := configureDashboardShare(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err == nil || !strings.Contains(err.Error(), "already proxies") {
		t.Fatalf("err=%v", err)
	}
}

func TestConfigureDashboardShareRejectsFunnel(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}},"AllowFunnel":{"agent.tailnet.ts.net:7412":true}}`), nil
	}
	err := configureDashboardShare(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412)
	if err == nil || !strings.Contains(err.Error(), "Funnel") {
		t.Fatalf("err=%v", err)
	}
}

func TestRemoteDashboardURLAndQR(t *testing.T) {
	value, err := remoteDashboardURL("agent.tailnet.ts.net", "http://127.0.0.1:7412/?token=secret", 7412)
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://agent.tailnet.ts.net:7412/?token=secret#conversations" {
		t.Fatalf("URL=%q", value)
	}
	path := filepath.Join(t.TempDir(), "share.png")
	if err := writeDashboardQR(value, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Size() == 0 {
		t.Fatalf("mode=%o size=%d", info.Mode().Perm(), info.Size())
	}
}

func TestDashboardShareTargetAlwaysUsesLoopbackIP(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7412", "localhost:7412", "[::1]:7412"} {
		target, err := dashboardShareTarget(listen)
		if err != nil {
			t.Fatal(err)
		}
		if target != "http://127.0.0.1:7412" {
			t.Fatalf("listen=%q target=%q", listen, target)
		}
	}
}

func TestDashboardRemoteEnabledRequiresHTTPS(t *testing.T) {
	config := daemon.DashboardConfig{Enabled: true}
	status := tailscaleServeStatus{Web: map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	}{"agent.tailnet.ts.net:7412": {Handlers: map[string]struct {
		Proxy string `json:"Proxy"`
	}{"/": {Proxy: "http://127.0.0.1:7412"}}}}}
	if dashboardRemoteEnabled(config, status, "agent.tailnet.ts.net", "http://127.0.0.1:7412", false) {
		t.Fatal("HTTP-only route reported enabled")
	}
	if !dashboardRemoteEnabled(config, status, "agent.tailnet.ts.net", "http://127.0.0.1:7412", true) {
		t.Fatal("HTTPS route reported disabled")
	}
}
