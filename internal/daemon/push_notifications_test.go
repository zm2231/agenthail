package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
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
	if _, err := sendDevicePush(context.Background(), target, mobileNotification{Title: "Agenthail", Message: "Session finished", SessionID: "codex/session", TurnID: "turn-42", EventType: "turn.completed"}); err != nil {
		t.Fatal(err)
	}
	if received["installationId"] != "install" || received["credential"] != "secret" || received["sessionId"] != "codex/session" || received["turnId"] != "turn-42" {
		t.Fatalf("payload=%+v", received)
	}
}

func TestSendDevicePushFailsWhenRelayIsNotConfigured(t *testing.T) {
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	previous := bundledPushRelayURL
	bundledPushRelayURL = ""
	t.Cleanup(func() { bundledPushRelayURL = previous })
	target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
	if _, err := sendDevicePush(context.Background(), target, mobileNotification{Title: "Agenthail", Message: "Session finished", SessionID: "codex/session", TurnID: "turn-42", EventType: "turn.completed"}); err == nil {
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
	shortenDevicePushRetries(t)
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
	if _, err := sendDevicePushWithRetry(context.Background(), target, mobileNotification{Title: "Agenthail", Message: "Session finished", SessionID: "codex/session", TurnID: "turn-42", EventType: "turn.completed"}); err != nil {
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
	if _, err := sendDevicePushWithRetry(context.Background(), target, mobileNotification{Title: "Agenthail", Message: "Session finished", SessionID: "codex/session", TurnID: "turn-42", EventType: "turn.completed"}); err == nil {
		t.Fatal("expected delivery error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestNotifyPairedDevicesRetiresOnlyTerminalTargets(t *testing.T) {
	shortenDevicePushRetries(t)
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
			device, _, err := reg.CompleteDevicePairing(pairing.Secret, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
				t.Fatal(err)
			}
			newTestDaemon(t, reg, nil).notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Finished", SessionID: "codex/session", TurnID: "turn-1", EventType: "turn.completed"})
			targets, err := reg.DevicePushTargets()
			if err != nil || len(targets) != test.remaining {
				t.Fatalf("targets=%+v err=%v", targets, err)
			}
		})
	}
}

func registryTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func shortenDevicePushRetries(t *testing.T) {
	previous := devicePushRetryDelay
	devicePushRetryDelay = time.Millisecond
	t.Cleanup(func() { devicePushRetryDelay = previous })
}

func TestAKeyedPushReachesARelayWithoutDedupe(t *testing.T) {
	shortenDevicePushRetries(t)
	for _, relay := range []struct {
		name       string
		refuseKeys bool
	}{{"relay from before dedupe", false}, {"relay missing its dedupe store", true}} {
		t.Run(relay.name, func(t *testing.T) {
			var delivered []map[string]any
			var refused atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if _, keyed := body["dedupeKey"]; keyed && relay.refuseKeys {
					refused.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					w.Write([]byte(`{"error":"dedupe_store_unavailable"}`))
					return
				}
				delivered = append(delivered, body)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
			key := completionDedupeKey("host", "session", "turn")
			target := registry.DevicePushTarget{InstallationID: "install", Credential: "secret"}
			if _, err := sendDevicePushWithRetry(context.Background(), target, mobileNotification{Title: "Agenthail", Message: "Agenthail finished on mini", SessionID: "session", TurnID: "turn", EventType: "turn.completed", DedupeKey: key}); err != nil {
				t.Fatal(err)
			}
			if len(delivered) != 1 {
				t.Fatalf("delivered %d pushes, want 1", len(delivered))
			}
			if relay.refuseKeys && refused.Load() != 1 {
				t.Fatalf("keyed sends refused %d times, want 1", refused.Load())
			}
		})
	}
}
