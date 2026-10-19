package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type signinRequest struct {
	Method string
	Path   string
	Query  string
	Body   map[string]any
}

type fakeSigninAPI struct {
	mu       sync.Mutex
	requests []signinRequest
}

func (f *fakeSigninAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	request := signinRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
	_ = json.Unmarshal(body, &request.Body)
	f.requests = append(f.requests, request)
	w.Header().Set("Content-Type", "application/json")
	profile := `{"id":"sp_0123456789ab","label":"Work Chrome","browser":"chrome","profile":"Profile 2","createdAt":"2026-10-18T00:00:00Z"}`
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/signin":
		_, _ = io.WriteString(w, `{"profiles":[`+profile+`],"consumers":[{"id":"notion","label":"Notion","purpose":"p","profileId":"sp_0123456789ab"},{"id":"claude","label":"Claude Remote Control","purpose":"p","profileId":null}],"browsers":[{"browser":"chrome","label":"Google Chrome","available":true,"profiles":[{"profile":"Default","name":"Personal"},{"profile":"Profile 2","name":"Work","profileId":"sp_0123456789ab"}]},{"browser":"edge","label":"Microsoft Edge","available":false,"detail":"reading this browser's profiles did not finish in time","profiles":[]}]}`)
	case "POST /api/v1/signin/profiles":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"profile":`+profile+`}`)
	case "PUT /api/v1/signin/bindings":
		if request.Body["profileId"] == nil {
			_, _ = io.WriteString(w, `{"consumer":"claude","profile":null}`)
			return
		}
		_, _ = io.WriteString(w, `{"consumer":"notion","profile":`+profile+`}`)
	case "DELETE /api/v1/signin/profiles":
		if r.URL.Query().Get("id") == "missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"signin_profile_not_found","message":"no sign-in profile matched \"missing\""}}`)
			return
		}
		_, _ = io.WriteString(w, `{"profile":`+profile+`}`)
	default:
		http.NotFound(w, r)
	}
}

func signinCLIFixture(t *testing.T) (*App, *fakeSigninAPI) {
	t.Helper()
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	api := &fakeSigninAPI{}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	return app, api
}

func TestSigninCommandsCallTheDaemonAPI(t *testing.T) {
	app, api := signinCLIFixture(t)
	for _, test := range []struct {
		args []string
		want []string
	}{
		{[]string{"signin"}, []string{"notion  Work Chrome (sp_0123456789ab)", "claude  not set up", `"Profile 2"  Work  added as Work Chrome`, "edge (Microsoft Edge): unavailable"}},
		{[]string{"signin", "add", "chrome", "Profile 2", "--label", "Work Chrome"}, []string{"Added Work Chrome (sp_0123456789ab, Google Chrome \"Profile 2\")", "agenthail signin use <notion|claude> sp_0123456789ab"}},
		{[]string{"signin", "use", "notion", "Work Chrome"}, []string{`Notion reads cookies from Work Chrome (Google Chrome "Profile 2")`}},
		{[]string{"signin", "use", "claude", "none"}, []string{"Claude Remote Control is not set up and reads no cookies"}},
		{[]string{"signin", "rm", "Work Chrome"}, []string{"Removed Work Chrome. Consumers that used it are not set up"}},
	} {
		output, err := captureStdout(t, func() error { return app.Run(test.args) })
		if err != nil {
			t.Fatalf("%v: err=%v output=%s", test.args, err, output)
		}
		for _, want := range test.want {
			if !strings.Contains(output, want) {
				t.Fatalf("%v: output=%q missing %q", test.args, output, want)
			}
		}
	}
	got := api.requests
	if len(got) != 5 {
		t.Fatalf("requests=%+v", got)
	}
	if got[1].Method != http.MethodPost || got[1].Body["browser"] != "chrome" || got[1].Body["profile"] != "Profile 2" || got[1].Body["label"] != "Work Chrome" {
		t.Fatalf("add=%+v", got[1])
	}
	if got[2].Method != http.MethodPut || got[2].Body["consumer"] != "notion" || got[2].Body["profileId"] != "Work Chrome" {
		t.Fatalf("use=%+v", got[2])
	}
	if _, hasProfile := got[3].Body["profileId"]; !hasProfile || got[3].Body["profileId"] != nil {
		t.Fatalf("use none must send profileId null: %+v", got[3])
	}
	if got[4].Method != http.MethodDelete || got[4].Query != "id=Work+Chrome" {
		t.Fatalf("rm=%+v", got[4])
	}
}

func TestSigninCommandReportsDaemonErrors(t *testing.T) {
	app, _ := signinCLIFixture(t)
	_, err := captureStdout(t, func() error { return app.Run([]string{"signin", "rm", "missing"}) })
	if err == nil || !strings.Contains(err.Error(), `no sign-in profile matched "missing"`) {
		t.Fatalf("err=%v", err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"signin", "use", "notion"}) }); err == nil || !strings.Contains(err.Error(), "usage: agenthail signin") {
		t.Fatalf("err=%v", err)
	}
}

func TestDoctorShowsUnsetSigninAsNotSetUpWithoutFailing(t *testing.T) {
	t.Setenv("AGENTHAIL_COOKIE_BRIDGE", "")
	app, reg := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	app.Surfaces = []SurfaceEntry{{Name: "notion", Surface: surfaces.NewNotion(reg, "", "")}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if err != nil {
		t.Fatalf("an unset sign-in profile failed doctor: err=%v output=%s", err, output)
	}
	for _, want := range []string{
		"[sign-in] notion: not set up, needed only for Notion",
		"fix: run `agenthail signin use notion <profile>` (see `agenthail signin list`) or open Settings > Browser Sign-in",
		"[sign-in] claude: not set up, needed only for Claude Remote Control",
		"[notion] capabilities",
		"not set up: Notion: choose a browser sign-in profile",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output=%q missing %q", output, want)
		}
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var report struct {
		OK       bool `json:"ok"`
		Surfaces []struct {
			OK       bool   `json:"ok"`
			NotSetUp string `json:"notSetUp"`
		} `json:"surfaces"`
		Signin []struct {
			Consumer signin.Consumer `json:"consumer"`
			Profile  *signin.Profile `json:"profile"`
		} `json:"signin"`
	}
	if err != nil || json.Unmarshal([]byte(output), &report) != nil || !report.OK || len(report.Surfaces) != 1 || !report.Surfaces[0].OK || report.Surfaces[0].NotSetUp == "" || len(report.Signin) != 2 || report.Signin[0].Profile != nil {
		t.Fatalf("err=%v output=%s", err, output)
	}
}
