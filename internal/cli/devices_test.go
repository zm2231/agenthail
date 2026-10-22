package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// registryPairingDaemon answers the daemon's pairing and device endpoints
// from a real registry, so a test can pair a device the way a phone does.
func registryPairingDaemon(t *testing.T, reg *registry.Registry) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	fail := func(w http.ResponseWriter, status int, err error) {
		write(w, status, map[string]any{"error": map[string]string{"code": "failed", "message": err.Error()}})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dashboard-synthetic" {
			fail(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		switch {
		case r.URL.Path == "/api/v1/pairings" && r.Method == http.MethodPost:
			var request struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			pairing, err := reg.CreateDevicePairing(request.Name, request.Scopes, 5*time.Minute)
			if err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			link := "agenthail://pair?" + url.Values{"endpoint": {"https://server.example.ts.net:7412"}, "secret": {pairing.Secret}}.Encode()
			write(w, http.StatusCreated, map[string]any{"id": pairing.ID, "expiresAt": pairing.ExpiresAt, "pairingURL": link})
		case r.URL.Path == "/api/v1/pairings" && r.Method == http.MethodGet:
			status, err := reg.PairingStatus(r.URL.Query().Get("id"))
			if err != nil {
				fail(w, http.StatusNotFound, err)
				return
			}
			write(w, http.StatusOK, status)
		case r.URL.Path == "/api/v1/devices" && r.Method == http.MethodGet:
			devices, err := reg.ListDevices(false)
			if err != nil {
				fail(w, http.StatusInternalServerError, err)
				return
			}
			write(w, http.StatusOK, map[string]any{"devices": devices})
		case r.URL.Path == "/api/v1/devices" && r.Method == http.MethodDelete:
			var request struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				fail(w, http.StatusBadRequest, err)
				return
			}
			if err := reg.RevokeDevice(request.ID); err != nil {
				fail(w, http.StatusNotFound, err)
				return
			}
			write(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			fail(w, http.StatusNotFound, errors.New("not found"))
		}
	}))
}

func devicesFixture(t *testing.T) (*App, *registry.Registry) {
	t.Helper()
	app, reg := cliFixture(t, &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}})
	server := registryPairingDaemon(t, reg)
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	previous := devicePairPoll
	devicePairPoll = 10 * time.Millisecond
	t.Cleanup(func() { devicePairPoll = previous })
	return app, reg
}

func TestDevicesPairShowsACodeAPhonePairsWithAndThenListsAndRemovesIt(t *testing.T) {
	app, reg := devicesFixture(t)
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan error, 1)
	go func() {
		done <- app.Run([]string{"devices", "pair", "--name", "Phone"})
		writer.Close()
	}()
	var output strings.Builder
	scanner := bufio.NewScanner(reader)
	paired := false
	for scanner.Scan() {
		line := scanner.Text()
		output.WriteString(line + "\n")
		link, found := strings.CutPrefix(strings.TrimSpace(line), "agenthail://pair?")
		if !found || paired {
			continue
		}
		values, err := url.ParseQuery(link)
		if err != nil {
			t.Error(err)
			continue
		}
		if _, _, err := reg.CompleteDevicePairing(values.Get("secret"), "Test iPhone", ""); err != nil {
			t.Errorf("the printed link did not pair: %v", err)
		}
		paired = true
	}
	os.Stdout = old
	reader.Close()
	if err := <-done; err != nil {
		t.Fatalf("devices pair: %v\n%s", err, output.String())
	}
	printed := output.String()
	if !paired || !strings.Contains(printed, "paired Test iPhone") {
		t.Fatalf("output=%s", printed)
	}
	if !strings.ContainsAny(printed, "█▀▄") || strings.Contains(printed, "\x1b[") {
		t.Fatalf("output has no plain QR code: %s", printed)
	}

	hostPairing, err := reg.CreateDevicePairing("Agenthail host", []string{"read", "control", registry.HostScope}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.CompleteDevicePairing(hostPairing.Secret, "laptop", "h0123456789ab"); err != nil {
		t.Fatal(err)
	}
	listed, err := captureStdout(t, func() error { return app.Run([]string{"devices"}) })
	if err != nil || !strings.Contains(listed, "Test iPhone") || strings.Contains(listed, "laptop") {
		t.Fatalf("devices list=%q err=%v, want the phone and not the paired host", listed, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"devices", "remove", "laptop"}) }); err == nil || !strings.Contains(err.Error(), "hosts remove") {
		t.Fatalf("removing a paired host as a device: err=%v", err)
	}
	removed, err := captureStdout(t, func() error { return app.Run([]string{"devices", "remove", "Test iPhone"}) })
	if err != nil || !strings.Contains(removed, "removed Test iPhone") {
		t.Fatalf("remove=%q err=%v", removed, err)
	}
	empty, err := captureStdout(t, func() error { return app.Run([]string{"devices", "list"}) })
	if err != nil || !strings.Contains(empty, "no paired devices") {
		t.Fatalf("after remove=%q err=%v", empty, err)
	}
}

func TestDevicesPairReportsACodeThatExpiredUnused(t *testing.T) {
	app, reg := devicesFixture(t)
	client, err := app.daemonAPIOverride()
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := reg.CreateDevicePairing("Phone", []string{"read", "control"}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := awaitPairing(client, devicePairing{ID: pairing.ID}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err=%v, want an expired code", err)
	}
}

func TestDevicesPairJSONPrintsTheCodeWithoutWaiting(t *testing.T) {
	app, _ := devicesFixture(t)
	output, err := captureStdout(t, func() error { return app.Run([]string{"devices", "pair", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var pairing devicePairing
	if err := json.Unmarshal([]byte(output), &pairing); err != nil || pairing.ID == "" || !strings.HasPrefix(pairing.PairingURL, "agenthail://pair?") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestTerminalQRPaintsItsOwnColorsOnlyOnATerminal(t *testing.T) {
	code := "█▀ \n▄█ \n"
	if plain := terminalQR(code, false); plain != code {
		t.Fatalf("plain=%q", plain)
	}
	painted := terminalQR(code, true)
	if strings.Count(painted, "\x1b[97;40m") != 2 || strings.Count(painted, "\x1b[0m\n") != 2 {
		t.Fatalf("painted=%q", painted)
	}
}

func TestDevicesPairSaysWhatToDoWhenItCannotShowACode(t *testing.T) {
	app, _ := devicesFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"remote_access_required","message":"Turn on remote access before pairing: run agenthail dashboard remote."}}`))
	}))
	t.Cleanup(server.Close)
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"devices", "pair"}) }); err == nil || !strings.Contains(err.Error(), "agenthail dashboard remote") {
		t.Fatalf("remote access off: err=%v", err)
	}
	app.catalogDaemonRunning = func() bool { return false }
	if _, err := captureStdout(t, func() error { return app.Run([]string{"devices", "pair"}) }); err == nil || !strings.Contains(err.Error(), "agenthail daemon start") {
		t.Fatalf("daemon stopped: err=%v", err)
	}
}
