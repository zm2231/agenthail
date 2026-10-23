package tailscale

import (
	"reflect"
	"strings"
	"testing"
)

func TestPublishMigratesOwnedHTTPRoute(t *testing.T) {
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
	if err := Publish(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err != nil {
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

func TestPublishRejectsOccupiedPort(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9000"}}}}}`), nil
	}
	if err := Publish(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err == nil || !strings.Contains(err.Error(), "already proxies") {
		t.Fatalf("err=%v", err)
	}
}

func TestPublishRejectsFunnel(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}},"AllowFunnel":{"agent.tailnet.ts.net:7412":true}}`), nil
	}
	err := Publish(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412)
	if err == nil || !strings.Contains(err.Error(), "Funnel") {
		t.Fatalf("err=%v", err)
	}
}

func TestUnpublishLeavesAnotherServicesRoute(t *testing.T) {
	var removed bool
	run := func(args ...string) ([]byte, error) {
		if len(args) == 3 && args[2] == "off" {
			removed = true
		}
		return []byte(`{"TCP":{"7412":{"HTTP":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9000"}}}}}`), nil
	}
	if _, err := Unpublish(run, "agent.tailnet.ts.net", "http://127.0.0.1:7412", 7412); err == nil || removed {
		t.Fatalf("err=%v removed=%v", err, removed)
	}
}

func TestURLIsTheServedHTTPSAddress(t *testing.T) {
	if value := URL("agent.tailnet.ts.net", 7412); value != "https://agent.tailnet.ts.net:7412/" {
		t.Fatalf("URL=%q", value)
	}
}

func TestTargetAlwaysUsesLoopbackIP(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7412", "localhost:7412", "[::1]:7412"} {
		target, err := Target(listen)
		if err != nil {
			t.Fatal(err)
		}
		if target != "http://127.0.0.1:7412" {
			t.Fatalf("listen=%q target=%q", listen, target)
		}
	}
}
