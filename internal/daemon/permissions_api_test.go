//go:build darwin

package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/permissions"
)

func TestPermissionsAPIReadsAndRequestsTheDaemonsGrants(t *testing.T) {
	macOS := filepath.Join(t.TempDir(), "Agenthail Daemon.app", "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	asked := filepath.Join(t.TempDir(), "asked")
	reporter := `#!/bin/sh
if [ "$1" = --ask ]; then echo "$@" > ` + asked + `; exit 0; fi
echo '{"accessibility":"missing","screenRecording":"granted","automation":{"com.openai.sky.CUAService":"missing"}}'
`
	if err := os.WriteFile(filepath.Join(macOS, permissions.Reporter), []byte(reporter), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(macOS, "agenthail")
	previous := permissionsDaemon
	permissionsDaemon = func() (permissions.Daemon, error) {
		return permissions.Daemon{Executable: exe, Service: permissions.Service}, nil
	}
	t.Cleanup(func() { permissionsDaemon = previous })

	d, _, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: "secret"}
	var report permissions.Report
	if code := api.do(http.MethodGet, "/api/v1/permissions", "", &report); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if report.Holder != filepath.Dir(filepath.Dir(macOS)) || report.Problem != "" || len(report.Grants) != 3 {
		t.Fatalf("report=%+v", report)
	}
	for i, state := range []string{permissions.Missing, permissions.Granted, permissions.Missing} {
		if report.Grants[i].State != state {
			t.Fatalf("grant %d = %+v", i, report.Grants[i])
		}
	}
	var answer map[string]bool
	if code := api.do(http.MethodPost, "/api/v1/permissions/request", "", &answer); code != http.StatusAccepted || !answer["asked"] {
		t.Fatalf("status=%d answer=%v", code, answer)
	}
	for i := 0; ; i++ {
		if data, _ := os.ReadFile(asked); len(data) > 0 {
			break
		}
		if i > 100 {
			t.Fatal("the reporter was not asked")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
