package cli

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakePNG(width, height uint32) []byte {
	image := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	image = binary.BigEndian.AppendUint32(image, width)
	image = binary.BigEndian.AppendUint32(image, height)
	return append(image, 8, 6, 0, 0, 0)
}

func snapshotFixture(t *testing.T, handler http.HandlerFunc) *App {
	t.Helper()
	app, _ := uiFixture(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	return app
}

func TestUISnapshotWritesTheAppImage(t *testing.T) {
	var asked map[string]any
	image := fakePNG(2880, 1800)
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ui/snapshot" || r.Header.Get("Authorization") != "Bearer dashboard-synthetic" {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&asked)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(image)
	})
	out := filepath.Join(t.TempDir(), "window.png")
	output, err := runUI(t, app, "snapshot", "--session", "s1", "--size", "1440x900", "--name", "Studio", out)
	if err != nil {
		t.Fatal(err)
	}
	if asked["session"] != "s1" || asked["width"] != float64(1440) || asked["height"] != float64(900) || asked["device"] != "Studio" {
		t.Fatalf("request = %v", asked)
	}
	written, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(written, image) {
		t.Fatalf("wrote %q, %v", written, err)
	}
	if !strings.Contains(output, "2880x1800 pixels") {
		t.Fatalf("output = %q", output)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(out), ".snapshot-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestUISnapshotReportsTheDaemonsReasonAndLeavesNoFile(t *testing.T) {
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"error":{"code":"snapshot_unanswered","message":"No Agenthail app answered."}}`))
	})
	out := filepath.Join(t.TempDir(), "window.png")
	if _, err := runUI(t, app, "snapshot", out); err == nil || !strings.Contains(err.Error(), "No Agenthail app answered") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a failed snapshot wrote %s: %v", out, err)
	}
	for _, size := range []string{"1440", "0x900", "axb", "9000x900"} {
		if _, err := runUI(t, app, "snapshot", "--size", size, out); err == nil || !strings.Contains(err.Error(), "--size") {
			t.Fatalf("--size %s: %v", size, err)
		}
	}
}
