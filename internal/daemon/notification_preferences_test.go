package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func preferencesCall(t *testing.T, d *Daemon, token, method, body string) (int, []notificationPreference) {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1/notification-preferences", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	dashboardRouter(d).ServeHTTP(response, request)
	var answer struct {
		Preferences []notificationPreference `json:"preferences"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &answer)
	return response.Code, answer.Preferences
}

func TestOnlyTheLeadsCompletionsPushInAProjectWithALeadWhileThePreferenceIsOn(t *testing.T) {
	mac, _, _ := widgetOnTwoHosts(t)
	pushes := newPushRecorder(t, mac.reg)
	logged := captureLog(mac.daemon)
	finished := turnCompletedNotice{title: "@worker", desktop: "Finished", mobile: "Finished", outcome: turnOutcomeFinished}

	mac.daemon.scheduleTurnPush("local-a", "a-1", finished)
	mac.daemon.background.wait()
	if count := pushes.count("turn.completed"); count != 0 {
		t.Fatalf("a worker's completion pushed %d times with only the lead's completions on", count)
	}
	if lines := settledLines(logged); len(lines) != 1 || !strings.Contains(lines[0], "suppressed=lead-only") {
		t.Fatalf("settled log %q", lines)
	}
	if notices, _ := mac.reg.TurnNotices(); notices["local-a"].TurnID == "a-1" {
		t.Fatal("a quieted completion was recorded for viewers to mirror")
	}

	mac.daemon.scheduleTurnPush("lead", "l-1", finished)
	mac.daemon.background.wait()
	if count := pushes.count("turn.completed"); count != 1 {
		t.Fatalf("the lead's completion pushed %d times", count)
	}

	if code, rows := preferencesCall(t, mac.daemon, dashboardTestToken, http.MethodPut, `{"id":"leadCompletionsOnly","value":false}`); code != http.StatusOK || len(rows) != 1 || rows[0].Value {
		t.Fatalf("turning the preference off answered %d %+v", code, rows)
	}
	mac.daemon.scheduleTurnPush("local-a", "a-2", finished)
	mac.daemon.background.wait()
	if count := pushes.count("turn.completed"); count != 2 {
		t.Fatalf("a worker's completion with the preference off pushed %d in all", count)
	}
}

func TestAWorkersQuestionReachesThePersonWhileOnlyTheLeadsCompletionsPush(t *testing.T) {
	mac, _, _ := widgetOnTwoHosts(t)
	pushes := newPushRecorder(t, mac.reg)
	mac.daemon.announceAsk(registry.Ask{ID: "ask-worker", SessionID: "local-a", Title: "Ship it?", RoutedTo: registry.AskToPerson, CreatedAt: time.Now()})
	mac.daemon.background.wait()
	if count := pushes.count("ask.opened"); count != 1 {
		t.Fatalf("a worker's question pushed %d times", count)
	}
	if !mac.daemon.notificationPreference(notificationPreferenceLeadCompletionsOnly) {
		t.Fatal("the preference is not on by default")
	}
}

func TestAPhoneReadsAndSetsNotificationPreferencesAndAnUnknownOneIsRefused(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	t.Setenv("HOME", t.TempDir())
	_, phone, err := d.Registry.CompleteDevicePairing(mustCreatePairing(t, d), "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	code, rows := preferencesCall(t, d, phone, http.MethodGet, "")
	if code != http.StatusOK || len(rows) != 1 || rows[0].ID != notificationPreferenceLeadCompletionsOnly || !rows[0].Value || !rows[0].Default || rows[0].Title == "" {
		t.Fatalf("a phone read %d %+v", code, rows)
	}
	if code, rows := preferencesCall(t, d, phone, http.MethodPut, `{"id":"leadCompletionsOnly","value":false}`); code != http.StatusOK || rows[0].Value {
		t.Fatalf("a phone turning it off answered %d %+v", code, rows)
	}
	if d.notificationPreference(notificationPreferenceLeadCompletionsOnly) {
		t.Fatal("the stored preference is still on")
	}
	if code, _ := preferencesCall(t, d, phone, http.MethodPut, `{"id":"everything","value":true}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown preference answered %d", code)
	}
	if code, _ := preferencesCall(t, d, phone, http.MethodPut, `{"id":"leadCompletionsOnly"}`); code != http.StatusBadRequest {
		t.Fatalf("a preference without a value answered %d", code)
	}
	config, err := LoadDashboardConfig()
	if err != nil || len(config.NotificationPreferences) != 1 || config.NotificationPreferences[notificationPreferenceLeadCompletionsOnly] {
		t.Fatalf("stored overrides %+v err=%v", config.NotificationPreferences, err)
	}
	if code, rows := preferencesCall(t, d, phone, http.MethodPut, `{"id":"leadCompletionsOnly","value":true}`); code != http.StatusOK || !rows[0].Value {
		t.Fatalf("turning it back on answered %d %+v", code, rows)
	}
	if config, err := LoadDashboardConfig(); err != nil || len(config.NotificationPreferences) != 0 {
		t.Fatalf("a preference set back to its default is still stored: %+v err=%v", config.NotificationPreferences, err)
	}
}
