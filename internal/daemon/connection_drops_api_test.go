package daemon

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"testing"
	"time"
)

func TestAPhonesConnectionDropsAreRecordedOnceAndInvalidOnesAreSkipped(t *testing.T) {
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	d.log = log.New(io.Discard, "", 0)
	handler := dashboardRouter(d)
	phone := deviceToken(t, reg, "Phone", "read")
	now := time.Now().UTC()
	drop := func(id string, change func(map[string]any)) map[string]any {
		row := map[string]any{"id": id, "host": "h00000000aaaa", "path": "stream probe", "startedAt": now.Add(-time.Minute).Format(time.RFC3339Nano),
			"recoveredAt": now.Add(-50 * time.Second).Format(time.RFC3339Nano), "recoveredBy": "snapshot", "firstError": "URLError.-1001",
			"lastError": "URLError.-1004", "elapsedMs": 15000, "attempts": 2, "shown": "offline", "sinceForegroundMs": 3000, "network": "wifi", "vpn": true}
		if change != nil {
			change(row)
		}
		return row
	}
	body := map[string]any{"drops": []map[string]any{
		drop("good0001", nil),
		drop("relay001", func(row map[string]any) {
			row["via"] = "h00000000aaaa"
			row["host"] = "h00000000bbbb"
			row["path"] = "request"
			row["recoveredBy"] = "peer connected"
		}),
		drop("future01", func(row map[string]any) { row["recoveredAt"] = now.Add(time.Hour).Format(time.RFC3339Nano) }),
		drop("BADID", nil),
		drop("badpath1", func(row map[string]any) { row["path"] = "teleport" }),
		drop("selfvia1", func(row map[string]any) { row["via"] = "h00000000aaaa" }),
		drop("backward", func(row map[string]any) { row["startedAt"] = now.Format(time.RFC3339Nano) }),
		drop("negative", func(row map[string]any) { row["elapsedMs"] = -1 }),
		drop("noattmpt", func(row map[string]any) { row["attempts"] = 0 }),
		drop("badhost1", func(row map[string]any) { row["host"] = "build-box" }),
	}}
	var reply struct {
		Recorded int `json:"recorded"`
	}
	response := apiCall(t, handler, http.MethodPost, "/api/v1/device/connection-drops", phone, body)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &reply) != nil || reply.Recorded != 3 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response = apiCall(t, handler, http.MethodPost, "/api/v1/device/connection-drops", phone, body); json.Unmarshal(response.Body.Bytes(), &reply) != nil || reply.Recorded != 0 {
		t.Fatalf("a repeated upload recorded again: %s", response.Body.String())
	}
	histories, err := reg.ConnectionDropHistories()
	if err != nil || len(histories) != 1 || len(histories[0].Drops) != 3 {
		t.Fatalf("histories=%+v err=%v", histories, err)
	}
	for _, kept := range histories[0].Drops {
		if kept.RecoveredAt.After(time.Now()) {
			t.Fatalf("a recovery in the future was kept as is: %+v", kept)
		}
		if kept.ID == "relay001" && (kept.Via != "h00000000aaaa" || kept.Host != "h00000000bbbb") {
			t.Fatalf("a relayed drop lost its route: %+v", kept)
		}
	}

	if response = apiCall(t, handler, http.MethodPost, "/api/v1/device/connection-drops", "not-a-token", body); response.Code != http.StatusUnauthorized {
		t.Fatalf("an unpaired caller got %d", response.Code)
	}
	if response = apiCall(t, handler, http.MethodGet, "/api/v1/device/connection-drops", phone, nil); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET got %d", response.Code)
	}
	var tooMany []map[string]any
	for range connectionDropBatch + 1 {
		tooMany = append(tooMany, drop("good0001", nil))
	}
	if response = apiCall(t, handler, http.MethodPost, "/api/v1/device/connection-drops", phone, map[string]any{"drops": tooMany}); response.Code != http.StatusBadRequest {
		t.Fatalf("an oversized batch got %d", response.Code)
	}
}
