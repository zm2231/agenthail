package daemon

import (
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
		filepath.Join(chrome, "Local State"):                                                                                   `{"profile":{"info_cache":{"Default":{"name":"Personal"},"Profile 2":{"name":"Work"}}}}`,
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

	var view signinView
	if code := api.do(http.MethodGet, "/api/v1/signin", "", &view); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if len(view.Profiles) != 0 || len(view.Consumers) != 2 || view.Consumers[0].ProfileID != nil || view.Consumers[1].ProfileID != nil {
		t.Fatalf("view=%+v", view)
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
	if _, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); !signin.IsNotConfigured(err) {
		t.Fatalf("an added profile bound itself: %v", err)
	}

	var bound struct {
		Consumer signin.Consumer `json:"consumer"`
		Profile  *signin.Profile `json:"profile"`
	}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":"Google Chrome Work"}`, &bound); code != http.StatusOK || bound.Profile == nil || bound.Profile.ID != added.Profile.ID {
		t.Fatalf("status=%d bound=%+v", code, bound)
	}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"claude","profileId":"`+firefox.Profile.ID+`"}`, &bound); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	source, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion})
	wantDir := filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Profile 2")
	if err != nil || source != (signin.Source{ProfileID: added.Profile.ID, Browser: signin.Chrome, Dir: wantDir}) {
		t.Fatalf("source=%+v err=%v", source, err)
	}
	source, err = r.ResolveSignin(signin.Target{Consumer: signin.Claude, SessionID: "conversation"})
	if err != nil || source.ProfileID != firefox.Profile.ID || source.Browser != signin.Firefox {
		t.Fatalf("source=%+v err=%v", source, err)
	}

	view = signinView{}
	api.do(http.MethodGet, "/api/v1/signin", "", &view)
	if len(view.Profiles) != 2 || view.Consumers[0].ID != signin.Notion || view.Consumers[0].ProfileID == nil || *view.Consumers[0].ProfileID != added.Profile.ID {
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
	if _, err := r.ResolveSignin(signin.Target{Consumer: signin.Notion}); !signin.IsNotConfigured(err) {
		t.Fatalf("removing a profile moved its consumer elsewhere: %v", err)
	}
	if source, err := r.ResolveSignin(signin.Target{Consumer: signin.Claude}); err != nil || source.ProfileID != firefox.Profile.ID {
		t.Fatalf("removing one profile changed another binding: %+v %v", source, err)
	}
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"claude","profileId":null}`, &bound); code != http.StatusOK || bound.Profile != nil {
		t.Fatalf("status=%d bound=%+v", code, bound)
	}
	if _, err := r.ResolveSignin(signin.Target{Consumer: signin.Claude}); !signin.IsNotConfigured(err) {
		t.Fatalf("clearing a binding left it set: %v", err)
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
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"codex","profileId":null}`, http.StatusBadRequest, "invalid_consumer"},
		{http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion"}`, http.StatusBadRequest, "invalid_request"},
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
	if code := api.do(http.MethodPut, "/api/v1/signin/bindings", `{"consumer":"notion","profileId":null}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("a control-only device changed sign-in settings: status=%d", code)
	}
}
