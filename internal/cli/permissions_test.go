package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/permissions"
	"github.com/zm2231/agenthail/internal/surface"
)

func permissionsCLIFixture(t *testing.T, running bool) (*App, *[]string) {
	t.Helper()
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/permissions":
			_, _ = io.WriteString(w, `{"supported":true,"holder":"/Library/Application Support/Agenthail/Agenthail Daemon.app","grants":[`+
				`{"name":"Accessibility","state":"granted","pane":"System Settings > Privacy & Security > Accessibility","url":"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"},`+
				`{"name":"Screen Recording","state":"missing","pane":"System Settings > Privacy & Security > Screen & System Audio Recording","url":"x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"},`+
				`{"name":"Automation","target":"com.openai.sky.CUAService","targetName":"Codex Computer Use","state":"denied","pane":"System Settings > Privacy & Security > Automation","url":"x-apple.systempreferences:com.apple.preference.security?Privacy_Automation"}]}`)
		case "POST /api/v1/permissions/request":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"asked":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return running }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	return app, &calls
}

func TestPermissionsShowsEachGrantAndItsSettingsPane(t *testing.T) {
	app, calls := permissionsCLIFixture(t, true)
	output, err := captureStdout(t, func() error { return app.Run([]string{"permissions"}) })
	if err != nil {
		t.Fatalf("err=%v output=%s", err, output)
	}
	for _, want := range []string{
		"[permissions] held by /Library/Application Support/Agenthail/Agenthail Daemon.app",
		"  Accessibility: granted",
		"  Screen Recording: missing; run 'agenthail permissions request' to be asked for it, or turn on Agenthail Daemon in System Settings > Privacy & Security > Screen & System Audio Recording",
		"  Automation of Codex Computer Use: denied; turn on Agenthail Daemon in System Settings > Privacy & Security > Automation",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output=%q missing %q", output, want)
		}
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"permissions", "request"}) })
	if err != nil || !strings.Contains(output, "macOS is asking") {
		t.Fatalf("err=%v output=%s", err, output)
	}
	if got := strings.Join(*calls, ","); got != "GET /api/v1/permissions,POST /api/v1/permissions/request" {
		t.Fatalf("calls=%s", got)
	}
}

func TestPermissionsRefusesAnUnknownFlag(t *testing.T) {
	app, calls := permissionsCLIFixture(t, true)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"permissions", "--jsn"}) }); err == nil || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"permissions", "--json"}) })
	var report permissions.Report
	if err != nil || json.Unmarshal([]byte(output), &report) != nil || len(report.Grants) != 3 {
		t.Fatalf("err=%v output=%s", err, output)
	}
}

func TestPermissionsNeedTheDaemonToReadItsOwnGrants(t *testing.T) {
	app, calls := permissionsCLIFixture(t, false)
	_, err := captureStdout(t, func() error { return app.Run([]string{"permissions"}) })
	if err == nil || !strings.Contains(err.Error(), "only it can read its own grants") || len(*calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, *calls)
	}
}

func TestDoctorReportsTheDaemonsGrantsOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("privacy grants are a macOS setting")
	}
	app, _ := permissionsCLIFixture(t, true)
	output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if !strings.Contains(output, "[permissions] held by") || !strings.Contains(output, "Automation of Codex Computer Use: denied") {
		t.Fatalf("output=%s", output)
	}
	output, _ = captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var report struct {
		Permissions permissions.Report `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || len(report.Permissions.Grants) != 3 || report.Permissions.Grants[1].State != permissions.Missing {
		t.Fatalf("err=%v output=%s", err, output)
	}
}
