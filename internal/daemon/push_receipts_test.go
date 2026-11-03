package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAPushTheRelayForwardsWithAnIDIsMarkedArrivedByThePhonesReceipt(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PushID string `json:"pushId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sent = append(sent, body.PushID)
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"receipt":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	d.log = log.New(io.Discard, "", 0)
	handler := dashboardRouter(d)
	phone := deviceToken(t, reg, "Phone", "read")
	other := deviceToken(t, reg, "Tablet", "read")
	devices, err := reg.ListDevices(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if device.Name != "Phone" && device.Name != "Tablet" {
			continue
		}
		if err := reg.SaveDevicePushTarget(device.ID, "installation-"+device.Name, "credential"); err != nil {
			t.Fatal(err)
		}
	}

	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Claude asks you a question", SessionID: "s1", EventType: "session.waiting"})
	if len(sent) != 2 || sent[0] == "" || sent[0] == sent[1] || !pushIDPattern.MatchString(sent[0]) {
		t.Fatalf("each device's push did not carry its own id: %q", sent)
	}

	receipts := map[string]any{"receipts": []map[string]any{{"pushId": sent[0], "receivedAt": time.Now().UTC().Format(time.RFC3339Nano), "alerts": "on"}, {"pushId": sent[1], "alerts": "on"}}}
	var phoneRecorded, otherRecorded struct {
		Recorded int `json:"recorded"`
	}
	response := apiCall(t, handler, http.MethodPost, "/api/v1/device/push-receipts", phone, receipts)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &phoneRecorded) != nil {
		t.Fatalf("receipt status=%d body=%s", response.Code, response.Body.String())
	}
	response = apiCall(t, handler, http.MethodPost, "/api/v1/device/push-receipts", other, receipts)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &otherRecorded) != nil {
		t.Fatalf("receipt status=%d body=%s", response.Code, response.Body.String())
	}
	if phoneRecorded.Recorded != 1 || otherRecorded.Recorded != 1 {
		t.Fatalf("each device should mark only its own push arrived: phone=%d other=%d", phoneRecorded.Recorded, otherRecorded.Recorded)
	}
	statuses, err := reg.PushStatuses(time.Now().Add(time.Hour))
	if err != nil || len(statuses) != 2 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	for _, status := range statuses {
		if status.ReceivedAt == "" || status.Alerts != "on" || status.Unarrived != 0 {
			t.Fatalf("a device that sent its receipt is not marked arrived: %+v", status)
		}
	}

	if response := apiCall(t, handler, http.MethodPost, "/api/v1/device/push-receipts", "not-a-token", receipts); response.Code != http.StatusUnauthorized {
		t.Fatalf("an unpaired caller was answered %d", response.Code)
	}
}

func TestAReceiptThatArrivesBeforeTheRelayAnswersStillCounts(t *testing.T) {
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	d.log = log.New(io.Discard, "", 0)
	handler := dashboardRouter(d)
	phone := deviceToken(t, reg, "Phone", "read")
	devices, _ := reg.ListDevices(false)
	for _, device := range devices {
		if device.Name == "Phone" {
			if err := reg.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var recorded struct {
		Recorded int `json:"recorded"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PushID string `json:"pushId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		response := apiCall(t, handler, http.MethodPost, "/api/v1/device/push-receipts", phone, map[string]any{"receipts": []map[string]any{{"pushId": body.PushID, "alerts": "on"}}})
		_ = json.Unmarshal(response.Body.Bytes(), &recorded)
		w.Write([]byte(`{"ok":true,"receipt":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)

	d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Claude asks you a question", SessionID: "s1", EventType: "session.waiting"})
	if recorded.Recorded != 1 {
		t.Fatalf("a receipt that beat the relay's answer was not recorded: %d", recorded.Recorded)
	}
	statuses, err := reg.PushStatuses(time.Now().Add(time.Hour))
	if err != nil || len(statuses) != 1 || statuses[0].ReceivedAt == "" || statuses[0].Unarrived != 0 {
		t.Fatalf("a push that arrived before the relay answered counts as lost: %+v err=%v", statuses, err)
	}
}

func TestAPushThroughARelayWithoutReceiptsExpectsNone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", server.URL)
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	d.log = log.New(io.Discard, "", 0)
	deviceToken(t, reg, "Phone", "read")
	devices, _ := reg.ListDevices(false)
	if err := reg.SaveDevicePushTarget(devices[0].ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		d.notifyPairedDevices(context.Background(), mobileNotification{Title: "Agenthail", Message: "Finished", SessionID: "s1", EventType: "turn.completed"})
	}
	statuses, err := reg.PushStatuses(time.Now().Add(time.Hour))
	if err != nil || len(statuses) != 1 || statuses[0].SucceededAt == "" || statuses[0].Unarrived != 0 {
		t.Fatalf("pushes an older relay took were counted as awaiting receipts: %+v err=%v", statuses, err)
	}
}
