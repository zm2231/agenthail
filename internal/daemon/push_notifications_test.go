package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestSendDevicePushUsesBoundedCapabilityPayload(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/send" || r.Method != http.MethodPost {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
	if err := sendDevicePush(context.Background(), target, "Agenthail", "Session finished", "codex/session", "turn.completed"); err != nil {
		t.Fatal(err)
	}
	if received["installationId"] != "install" || received["credential"] != "secret" || received["sessionId"] != "codex/session" {
		t.Fatalf("payload=%+v", received)
	}
}

func TestSendDevicePushFailsWhenRelayIsNotConfigured(t *testing.T) {
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	previous := bundledPushRelayURL
	bundledPushRelayURL = ""
	t.Cleanup(func() { bundledPushRelayURL = previous })
	target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
	if err := sendDevicePush(context.Background(), target, "Agenthail", "Session finished", "codex/session", "turn.completed"); err == nil || err.Error() != "push relay is not configured" {
		t.Fatalf("error=%v", err)
	}
}

func TestPushRelayURLUsesBuildDefaultAndEnvironmentOverride(t *testing.T) {
	previous := bundledPushRelayURL
	bundledPushRelayURL = "https://packaged.example.test/"
	t.Cleanup(func() { bundledPushRelayURL = previous })
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	if got := PushRelayURL(); got != "https://packaged.example.test" {
		t.Fatalf("packaged URL=%q", got)
	}
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "https://self-hosted.example.test/")
	if got := PushRelayURL(); got != "https://self-hosted.example.test" {
		t.Fatalf("override URL=%q", got)
	}
}

func TestSendDevicePushRetriesTransientFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			http.Error(w, "try again", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
	if err := sendDevicePushWithRetry(context.Background(), target, "Agenthail", "Session finished", "codex/session", "turn.completed"); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestSendDevicePushDoesNotRetryTerminalFailures(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
	if err := sendDevicePushWithRetry(context.Background(), target, "Agenthail", "Session finished", "codex/session", "turn.completed"); err == nil {
		t.Fatal("expected delivery error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestNotifyPairedDevicesRetiresOnlyTerminalTargets(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		remaining int
	}{
		{name: "terminal", status: http.StatusUnauthorized, remaining: 0},
		{name: "rate limited", status: http.StatusTooManyRequests, remaining: 1},
		{name: "retryable", status: http.StatusServiceUnavailable, remaining: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, http.StatusText(test.status), test.status)
			}))
			defer server.Close()
			t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
			reg := registryTestRegistry(t)
			pairing, err := reg.CreateDevicePairing("Phone", nil, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			device, _, err := reg.CompleteDevicePairing(pairing.Secret, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
				t.Fatal(err)
			}
			New(reg, nil).notifyPairedDevices(context.Background(), "Agenthail", "Finished", "codex/session", "turn.completed")
			targets, err := reg.DevicePushTargets()
			if err != nil || len(targets) != test.remaining {
				t.Fatalf("targets=%+v err=%v", targets, err)
			}
		})
	}
}

func registryTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}
