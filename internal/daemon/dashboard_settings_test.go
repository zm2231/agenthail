package daemon

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	registrypkg "github.com/zm2231/agenthail/internal/registry"
)

func TestDashboardConfigChangesOnlyTheFieldsItSends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := registrypkg.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	handler := dashboardRouter(newTestDaemon(t, r, nil))
	if err := dashconfig.Save(dashconfig.Config{CodexRecentHours: 12, BusyDelivery: deliverypolicy.Steer}); err != nil {
		t.Fatal(err)
	}
	post := func(body string, status int) {
		t.Helper()
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", body); response.Code != status {
			t.Fatalf("%s: status=%d body=%s", body, response.Code, response.Body.String())
		}
	}
	saved := func(hours int, delivery deliverypolicy.Mode) {
		t.Helper()
		config, err := dashconfig.Load()
		if err != nil {
			t.Fatal(err)
		}
		if config.CodexRecentHours != hours || config.BusyDelivery != delivery {
			t.Fatalf("saved codexRecentHours=%d busyDelivery=%s, want %d %s", config.CodexRecentHours, config.BusyDelivery, hours, delivery)
		}
	}

	post(`{"action":"dashboard-config","busyDelivery":"queue"}`, http.StatusOK)
	saved(12, deliverypolicy.Queue)
	post(`{"action":"dashboard-config","codexRecentHours":48}`, http.StatusOK)
	saved(48, deliverypolicy.Queue)
	post(`{"action":"dashboard-config","codexRecentHours":2,"busyDelivery":"steer"}`, http.StatusOK)
	saved(2, deliverypolicy.Steer)
	post(`{"action":"dashboard-config"}`, http.StatusBadRequest)
	post(`{"action":"dashboard-config","codexRecentHours":500}`, http.StatusBadRequest)
	saved(2, deliverypolicy.Steer)
}

func TestSingleValueSettingsRefuseAMissingValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := registrypkg.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	handler := dashboardRouter(newTestDaemon(t, r, nil))
	off := false
	if err := dashconfig.Save(dashconfig.Config{CodexRecentHours: 5, ResumeAfterLimit: &off, MoveAfterLimit: &off}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"resume-after-limit", "move-after-limit", "claude-default-root"} {
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", `{"action":"`+action+`"}`); response.Code != http.StatusBadRequest {
			t.Fatalf("%s with no value: status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
	config, err := dashconfig.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.ResumeAfterLimit == nil || *config.ResumeAfterLimit || config.MoveAfterLimit == nil || *config.MoveAfterLimit {
		t.Fatalf("a refused request changed the saved settings: resume=%v move=%v", config.ResumeAfterLimit, config.MoveAfterLimit)
	}
}
