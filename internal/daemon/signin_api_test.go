package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/signin"
)

func signinFixtureHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the browser fixtures use macOS profile locations")
	}
	home := t.TempDir()
	chrome := filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	for path, content := range map[string]string{
		filepath.Join(chrome, "Local State"): `{"profile":{"last_used":"Default","info_cache":{"Default":{"name":"Personal"},"Profile 2":{"name":"Work"}}}}`,
		filepath.Join(home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist"):     `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>LSHandlers</key><array><dict><key>LSHandlerURLScheme</key><string>https</string><key>LSHandlerRoleAll</key><string>com.google.chrome</string></dict></array></dict></plist>`,
		filepath.Join(chrome, "Default", "Network", "Cookies"):                                                                 "",
		filepath.Join(chrome, "Profile 2", "Network", "Cookies"):                                                               "",
		filepath.Join(home, "Library", "Application Support", "Firefox", "profiles.ini"):                                       "[Profile0]\nName=default-release\nIsRelative=1\nPath=Profiles/abcd.default-release\n",
		filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles", "abcd.default-release", "cookies.sqlite"): "",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	return home
}

type signinAPIClient struct {
	t       *testing.T
	handler http.Handler
	token   string
}

func (c signinAPIClient) do(method, path, body string, out any) int {
	c.t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	c.handler.ServeHTTP(response, request)
	if out != nil {
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, response.Body.String())
		}
	}
	return response.Code
}

type signinAPIError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestSigninAPIAddListUseRemove(t *testing.T) {
	home := signinFixtureHome(t)
	d, r, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	chromeRoot := filepath.Join(home, "Library", "Application Support", "Google", "Chrome")

	var view signinView
	if code := api.do(http.MethodGet, "/api/v1/signin", "", &view); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	automatic := signin.Status{Mode: signin.Automatic, Browser: signin.Chrome, BrowserLabel: "Google Chrome", Profile: "Default", ProfileName: "Personal"}
	for _, consumer := range view.Consumers {
		consumer.ConsumerInfo = signin.ConsumerInfo{}
		if consumer.ProfileID != nil || consumer != automatic {
			t.Fatalf("a consumer with no choice does not use the default browser: %+v", consumer)
		}
	}
	if len(view.Profiles) != 0 || len(view.Consumers) != 2 {
		t.Fatalf("view=%+v", view)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); err != nil || source != (signin.Source{Label: "Personal", Browser: signin.Chrome, Dir: filepath.Join(chromeRoot, "Default")}) {
		t.Fatalf("source=%+v err=%v", source, err)
	}
	var chrome *signinBrowserView
	for i := range view.Browsers {
		if view.Browsers[i].Browser == signin.Chrome {
			chrome = &view.Browsers[i]
		}
	}
	if chrome == nil || len(chrome.Profiles) != 2 || chrome.Profiles[1].Profile != "Profile 2" || chrome.Profiles[1].Name != "Work" {
		t.Fatalf("browsers=%+v", view.Browsers)
	}

	var added struct {
		Profile signin.Profile `json:"profile"`
	}
	if code := api.do(http.MethodPost, "/api/v1/signin/profiles", `{"browser":"chrome","profile":"Profile 2"}`, &added); code != http.StatusCreated {
		t.Fatalf("status=%d", code)
	}
	if !strings.HasPrefix(added.Profile.ID, "sp_") || added.Profile.Label != "Google Chrome Work" || added.Profile.Browser != signin.Chrome || added.Profile.Profile != "Profile 2" {
		t.Fatalf("profile=%+v", added.Profile)
	}
	var firefox struct {
		Profile signin.Profile `json:"profile"`
	}
	if code := api.do(http.MethodPost, "/api/v1/signin/profiles", `{"browser":"firefox","profile":"Profiles/abcd.default-release","label":"Firefox main"}`, &firefox); code != http.StatusCreated {
		t.Fatalf("status=%d", code)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); err != nil || source.ProfileID != "" {
		t.Fatalf("an added profile bound itself: %+v %v", source, err)
	}

	var bound signin.Status
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":"Google Chrome Work"}`, &bound); code != http.StatusOK || bound.Mode != signin.Chosen || bound.ProfileID == nil || *bound.ProfileID != added.Profile.ID || bound.ProfileName != "Google Chrome Work" {
		t.Fatalf("status=%d bound=%+v", code, bound)
	}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"claude","profileId":"`+firefox.Profile.ID+`"}`, &bound); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion})
	if err != nil || source != (signin.Source{ProfileID: added.Profile.ID, Label: added.Profile.Label, Browser: signin.Chrome, Dir: filepath.Join(chromeRoot, "Profile 2")}) {
		t.Fatalf("source=%+v err=%v", source, err)
	}
	source, err = r.ResolveSignin(signin.Target{Consumer: signin.Claude})
	if err != nil || source.ProfileID != firefox.Profile.ID || source.Browser != signin.Firefox {
		t.Fatalf("source=%+v err=%v", source, err)
	}

	view = signinView{}
	api.do(http.MethodGet, "/api/v1/signin", "", &view)
	if len(view.Profiles) != 2 || view.Consumers[0].ID != signin.Notion || view.Consumers[0].Mode != signin.Chosen || view.Consumers[0].ProfileID == nil || *view.Consumers[0].ProfileID != added.Profile.ID {
		t.Fatalf("view=%+v", view)
	}
	for _, browser := range view.Browsers {
		if browser.Browser == signin.Chrome && browser.Profiles[1].ProfileID != added.Profile.ID {
			t.Fatalf("detected profile does not show it was added: %+v", browser)
		}
	}

	if code := api.do(http.MethodDelete, "/api/v1/signin/profiles?id="+added.Profile.ID, "", nil); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); err != nil || source != (signin.Source{Label: "Personal", Browser: signin.Chrome, Dir: filepath.Join(chromeRoot, "Default")}) {
		t.Fatalf("removing a profile did not return its consumer to the default browser: %+v %v", source, err)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Claude}); err != nil || source.ProfileID != firefox.Profile.ID {
		t.Fatalf("removing one profile changed another binding: %+v %v", source, err)
	}

	bound = signin.Status{}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"claude","profileId":"none"}`, &bound); code != http.StatusOK || bound.Mode != signin.Off || bound.ProfileID != nil || bound.Browser != "" {
		t.Fatalf("status=%d bound=%+v", code, bound)
	}
	if _, err := r.ResolveSignin(signin.Target{Consumer: signin.Claude}); !signin.IsOff(err) {
		t.Fatalf("turning sign-in off still resolved a browser: %v", err)
	}
	bound = signin.Status{}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"claude","profileId":"auto"}`, &bound); code != http.StatusOK || bound.Mode != signin.Automatic || bound.Browser != signin.Chrome || bound.Profile != "Default" {
		t.Fatalf("status=%d bound=%+v", code, bound)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Claude}); err != nil || source.Dir != filepath.Join(chromeRoot, "Default") {
		t.Fatalf("auto did not return to the default browser: %+v %v", source, err)
	}
}

func TestSigninAPIReportsAnUnreadableDefaultBrowserWithTheFix(t *testing.T) {
	home := signinFixtureHome(t)
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	d, _, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	cookies := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "Network", "Cookies")
	if err := os.Chmod(cookies, 0); err != nil {
		t.Fatal(err)
	}
	var view signinView
	api.do(http.MethodGet, "/api/v1/signin", "", &view)
	notion := view.Consumers[0]
	if notion.Mode != signin.Automatic || notion.Browser != signin.Chrome || notion.Profile != "Default" || !strings.Contains(notion.Problem, "permission denied reading Google Chrome data at "+cookies) || !strings.Contains(notion.Problem, "make it readable") || strings.Contains(notion.Problem, "no cookie") {
		t.Fatalf("notion=%+v", notion)
	}
}

func TestSigninAPIRejectsProfilesThatAreNotOnThisComputer(t *testing.T) {
	signinFixtureHome(t)
	d, r, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: "secret"}
	for _, test := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{http.MethodPost, "/api/v1/signin/profiles", `{"browser":"opera","profile":"Default"}`, http.StatusBadRequest, "invalid_browser"},
		{http.MethodPost, "/api/v1/signin/profiles", `{"browser":"chrome","profile":"Profile 9"}`, http.StatusBadRequest, "browser_profile_not_found"},
		{http.MethodPost, "/api/v1/signin/profiles", `{"browser":"brave","profile":"Default"}`, http.StatusBadRequest, "browser_profile_not_found"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"codex","profileId":"auto"}`, http.StatusBadRequest, "invalid_consumer"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion"}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":null}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":""}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":"sp_000000000000"}`, http.StatusNotFound, "signin_profile_not_found"},
		{http.MethodDelete, "/api/v1/signin/profiles?id=missing", "", http.StatusNotFound, "signin_profile_not_found"},
	} {
		var failure signinAPIError
		if code := api.do(test.method, test.path, test.body, &failure); code != test.status || failure.Error.Code != test.code {
			t.Fatalf("%s %s %s: status=%d error=%+v", test.method, test.path, test.body, code, failure.Error)
		}
	}
	var added struct {
		Profile signin.Profile `json:"profile"`
	}
	api.do(http.MethodPost, "/api/v1/signin/profiles", `{"browser":"chrome","profile":"Default"}`, &added)
	api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":"`+added.Profile.ID+`"}`, nil)
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("a binding change without profileId was accepted: status=%d", code)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); err != nil || source.ProfileID != added.Profile.ID {
		t.Fatalf("a rejected binding change altered the binding: %+v %v", source, err)
	}
	var failure signinAPIError
	if code := api.do(http.MethodPost, "/api/v1/signin/profiles", `{"browser":"chrome","profile":"Default","label":"Again"}`, &failure); code != http.StatusConflict || !strings.Contains(failure.Error.Message, "already added") {
		t.Fatalf("status=%d error=%+v", code, failure.Error)
	}
}

func TestSigninAPINeedsTheSettingsScope(t *testing.T) {
	signinFixtureHome(t)
	d, _, _, _, _ := daemonFixture(t)
	_, token, err := d.Registry.CompleteDevicePairing(mustCreatePairing(t, d), "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	api := signinAPIClient{t: t, handler: d.dashboardHandler(&dashboardServer{token: "secret"}), token: token}
	if code := api.do(http.MethodGet, "/api/v1/signin", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("a control-only device read sign-in settings: status=%d", code)
	}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":"none"}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("a control-only device changed sign-in settings: status=%d", code)
	}
}

func TestASurfaceOnAComputerWithNoBrowserReportsTheStatedLimit(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	limit := signin.UnavailableError{Consumer: signin.Notion}
	entry := d.dashboardSurfaceHealth(context.Background(), fake, limit)
	if entry.Health != "signin_unavailable" || entry.HealthDetail != limit.Error() || strings.Contains(entry.HealthDetail, "xdg-settings") {
		t.Fatalf("entry=%+v", entry)
	}
}
