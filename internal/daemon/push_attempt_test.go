package daemon

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEveryPushAttemptIsLoggedAndItsOutcomeKeptPerDevice(t *testing.T) {
	shortenDevicePushRetries(t)
	var failing atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, `{"error":"apns_rejected"}`, http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)

	question := mobileNotification{Title: "Agenthail", Message: "Claude asks you a question", SessionID: "s1", EventType: "session.waiting", CollapseID: "k1", RequestID: "s1:question:held:x-1"}
	d.notifyPairedDevices(context.Background(), question)
	if line := logged.String(); !strings.Contains(line, "push session.waiting session=s1 request=s1:question:held:x-1 key=k1 devices=0 delivered=0 failed=0 (no paired device takes notifications)") {
		t.Fatalf("a push with no device left no line saying so: %q", line)
	}

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
	logged.Reset()
	d.notifyPairedDevices(context.Background(), question)
	if line := logged.String(); !strings.Contains(line, "devices=1 delivered=1 failed=0") || strings.Contains(line, "errors:") {
		t.Fatalf("a delivered push was not logged as delivered: %q", line)
	}
	statuses, err := reg.PushStatuses()
	if err != nil || len(statuses) != 1 || statuses[0].SucceededAt == "" || statuses[0].Error != "" || statuses[0].EventType != "session.waiting" {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	delivered := statuses[0].SucceededAt

	failing.Store(true)
	logged.Reset()
	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Finished", SessionID: "s1", EventType: "turn.completed"})
	if line := logged.String(); !strings.Contains(line, "push turn.completed session=s1 request=- devices=1 delivered=0 failed=1 errors: "+device.ID[:8]+": relay returned 502") {
		t.Fatalf("a failed push did not name its device and the relay's answer: %q", line)
	}
	statuses, err = reg.PushStatuses()
	if err != nil || len(statuses) != 1 || statuses[0].SucceededAt != delivered || !strings.Contains(statuses[0].Error, "relay returned 502") || statuses[0].FailedAt == "" || statuses[0].EventType != "turn.completed" {
		t.Fatalf("a failure did not keep the last delivery beside it: %+v err=%v", statuses, err)
	}

	logged.Reset()
	d.withdrawFromPairedDevices(context.Background(), "k1")
	if line := logged.String(); !strings.Contains(line, "push withdraw key=k1 devices=1 delivered=0 failed=1") {
		t.Fatalf("a withdrawal was not logged: %q", line)
	}
}
