package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type snapshotCall struct {
	code int
	body []byte
	kind string
}

func snapshotPost(handler http.Handler, path, contentType string, body []byte) snapshotCall {
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return snapshotCall{code: response.Code, body: response.Body.Bytes(), kind: response.Header().Get("Content-Type")}
}

func nextSnapshotRequest(t *testing.T, stream <-chan apiEvent) uiSnapshotEvent {
	t.Helper()
	for {
		select {
		case event := <-stream:
			if event.Type != "ui.snapshot.requested" {
				continue
			}
			var request uiSnapshotEvent
			if err := json.Unmarshal(event.Data, &request); err != nil {
				t.Fatal(err)
			}
			return request
		case <-time.After(5 * time.Second):
			t.Fatal("no snapshot request was published")
		}
	}
}

func TestUISnapshotRelaysTheFirstAppImageToTheCaller(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	_, stream, _, cancel := d.events.subscribe(d.events.cursor())
	defer cancel()

	image := append(append([]byte(nil), pngSignature...), []byte("pixels")...)
	done := make(chan snapshotCall, 1)
	go func() {
		done <- snapshotPost(handler, "/api/v1/ui/snapshot", "application/json", []byte(`{"session":"s1","width":900,"height":600,"device":"Studio"}`))
	}()
	request := nextSnapshotRequest(t, stream)
	store, err := d.ui.open()
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if request.Rev == "" || request.Rev != current.Rev {
		t.Fatalf("the request names ui.conf revision %q, want the current %q", request.Rev, current.Rev)
	}
	if request.ID == "" || request.Session != "s1" || request.Width != 900 || request.Height != 600 || request.Device != "Studio" || request.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("published request = %+v", request)
	}

	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "image/png", []byte("not a png")); call.code != http.StatusUnsupportedMediaType {
		t.Fatalf("a non-PNG upload was accepted: %d %s", call.code, call.body)
	}
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "image/png", image); call.code != http.StatusNoContent {
		t.Fatalf("first upload: %d %s", call.code, call.body)
	}
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "image/png", image); call.code != http.StatusConflict {
		t.Fatalf("a second app's upload was accepted: %d %s", call.code, call.body)
	}
	call := <-done
	if call.code != http.StatusOK || call.kind != "image/png" || !bytes.Equal(call.body, image) {
		t.Fatalf("caller got %d %q %q", call.code, call.kind, call.body)
	}
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/unknown", "image/png", image); call.code != http.StatusConflict {
		t.Fatalf("an upload for no request was accepted: %d", call.code)
	}
}

func TestUISnapshotExplainsWhenNoAppAnswers(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	saved := uiSnapshotTimeout
	uiSnapshotTimeout = 50 * time.Millisecond
	defer func() { uiSnapshotTimeout = saved }()

	call := snapshotPost(handler, "/api/v1/ui/snapshot", "application/json", []byte(`{}`))
	if call.code != http.StatusGatewayTimeout || !strings.Contains(string(call.body), "snapshot_unanswered") || !strings.Contains(string(call.body), "Mac app") {
		t.Fatalf("unanswered snapshot: %d %s", call.code, call.body)
	}
	if len(d.snapshots.pending) != 0 {
		t.Fatalf("an unanswered request stayed pending: %v", d.snapshots.pending)
	}
}

func TestUISnapshotRefusesBadSizesAndOversizedImages(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, body := range []string{`{"width":900}`, `{"width":-1,"height":600}`, `{"width":9000,"height":600}`, `not json`, `{} trailing`} {
		if call := snapshotPost(handler, "/api/v1/ui/snapshot", "application/json", []byte(body)); call.code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", body, call.code)
		}
	}
	id, _ := d.snapshots.open()
	huge := append(append([]byte(nil), pngSignature...), make([]byte, uiSnapshotMaxBytes)...)
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+id, "image/png", huge); call.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: %d", call.code)
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/v1/ui/snapshot", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("a snapshot without a token: %d", response.Code)
	}
}

func TestUISnapshotPassesTheAppsFailureToTheCaller(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	_, stream, _, cancel := d.events.subscribe(d.events.cursor())
	defer cancel()

	done := make(chan snapshotCall, 1)
	go func() { done <- snapshotPost(handler, "/api/v1/ui/snapshot", "application/json", []byte(`{}`)) }()
	request := nextSnapshotRequest(t, stream)
	for _, body := range []string{`{"error":" "}`, `{"error":"missing"} trailing`, `{"error":"a"}{"error":"b"}`} {
		if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "application/json", []byte(body)); call.code != http.StatusBadRequest {
			t.Fatalf("a malformed failure %s was accepted: %d %s", body, call.code, call.body)
		}
	}
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "application/json", []byte(`{"error":"This macOS no longer provides CGWindowListCreateImage."}`)); call.code != http.StatusNoContent {
		t.Fatalf("failure report: %d %s", call.code, call.body)
	}
	call := <-done
	if call.code != http.StatusBadGateway || !strings.Contains(string(call.body), "snapshot_failed") || !strings.Contains(string(call.body), "CGWindowListCreateImage") {
		t.Fatalf("caller got %d %s", call.code, call.body)
	}
	if call := snapshotPost(handler, "/api/v1/ui/snapshot/"+request.ID, "application/json", []byte(`{"error":"late"}`)); call.code != http.StatusConflict {
		t.Fatalf("a failure after the answer was accepted: %d", call.code)
	}
}

func TestUISnapshotWaitsForAChangeMadeOutsideTheDaemon(t *testing.T) {
	saved := uiSnapshotTimeout
	uiSnapshotTimeout = 200 * time.Millisecond
	defer func() { uiSnapshotTimeout = saved }()
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	store, err := d.ui.open()
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	d.noteUIRevision(before.Rev)
	_, stream, _, cancel := d.events.subscribe(d.events.cursor())
	defer cancel()

	changed, _, err := store.Write("version = 1\ntheme = dark\n", "agent", "Dark")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan snapshotCall, 1)
	go func() { done <- snapshotPost(handler, "/api/v1/ui/snapshot", "application/json", []byte(`{}`)) }()
	defer func() { <-done }()
	announced := ""
	for request := (uiSnapshotEvent{}); request.ID == ""; {
		select {
		case event := <-stream:
			switch event.Type {
			case "ui.changed":
				var change map[string]string
				_ = json.Unmarshal(event.Data, &change)
				announced = change["rev"]
			case "ui.snapshot.requested":
				_ = json.Unmarshal(event.Data, &request)
				if request.Rev != changed.Rev || announced != changed.Rev {
					t.Fatalf("request names %q after announcing %q, want both %q", request.Rev, announced, changed.Rev)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no snapshot request was published")
		}
	}
}
