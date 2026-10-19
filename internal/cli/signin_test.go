package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
		_, _ = io.WriteString(w, `{"profiles":[`+profile+`],"consumers":[{"id":"notion","label":"Notion","purpose":"p","profileId":"sp_0123456789ab","mode":"profile","browser":"chrome","browserLabel":"Google Chrome","profile":"Profile 2","profileName":"Work Chrome"},{"id":"claude","label":"Claude Remote Control","purpose":"p","profileId":null,"mode":"automatic","browser":"safari","browserLabel":"Safari","profile":"Default","profileName":"Safari","problem":"permission denied reading Safari data at /fixture/Cookies.binarycookies (macOS needs Full Disk Access for this)"}],"browsers":[{"browser":"chrome","label":"Google Chrome","available":true,"profiles":[{"profile":"Default","name":"Personal"},{"profile":"Profile 2","name":"Work","profileId":"sp_0123456789ab"}]},{"browser":"edge","label":"Microsoft Edge","available":false,"detail":"reading this browser's profiles did not finish in time","profiles":[]}]}`)
	case "POST /api/v1/signin/profiles":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"profile":`+profile+`}`)
	case "PUT /api/v1/signin/bindings":
		switch request.Body["profileId"] {
		case "none":
			_, _ = io.WriteString(w, `{"id":"claude","label":"Claude Remote Control","purpose":"p","profileId":null,"mode":"off"}`)
		case "auto":
			_, _ = io.WriteString(w, `{"id":"claude","label":"Claude Remote Control","purpose":"p","profileId":null,"mode":"automatic","browser":"chrome","browserLabel":"Google Chrome","profile":"Default","profileName":"Personal"}`)
		default:
			_, _ = io.WriteString(w, `{"id":"notion","label":"Notion","purpose":"p","profileId":"sp_0123456789ab","mode":"profile","browser":"chrome","browserLabel":"Google Chrome","profile":"Profile 2","profileName":"Work Chrome"}`)
		}
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
		{[]string{"signin"}, []string{`notion  Work Chrome (Google Chrome "Profile 2")`, "claude  Safari, automatic (system default browser)", "problem: permission denied reading Safari data", "change: agenthail signin use <notion|claude> <profile|auto|none>", `"Profile 2"  Work  added as Work Chrome`, "edge (Microsoft Edge): unavailable"}},
		{[]string{"signin", "add", "chrome", "Profile 2", "--label", "Work Chrome"}, []string{"Added Work Chrome (sp_0123456789ab, Google Chrome \"Profile 2\")", "agenthail signin use <notion|claude> sp_0123456789ab"}},
		{[]string{"signin", "use", "notion", "Work Chrome"}, []string{`Notion reads cookies from Work Chrome (Google Chrome "Profile 2")`}},
		{[]string{"signin", "use", "claude", "none"}, []string{"Claude Remote Control sign-in is off and reads no cookies"}},
		{[]string{"signin", "use", "claude", "auto"}, []string{`Claude Remote Control reads cookies from Google Chrome "Default" (Personal), automatic (system default browser)`}},
		{[]string{"signin", "rm", "Work Chrome"}, []string{"Removed Work Chrome. Consumers that used it read the system default browser again"}},
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
	if len(got) != 6 {
		t.Fatalf("requests=%+v", got)
	}
	if got[1].Method != http.MethodPost || got[1].Body["browser"] != "chrome" || got[1].Body["profile"] != "Profile 2" || got[1].Body["label"] != "Work Chrome" {
		t.Fatalf("add=%+v", got[1])
	}
	if got[2].Method != http.MethodPut || got[2].Body["consumer"] != "notion" || got[2].Body["profileId"] != "Work Chrome" {
		t.Fatalf("use=%+v", got[2])
	}
	if got[3].Body["profileId"] != "none" || got[4].Body["profileId"] != "auto" {
		t.Fatalf("use none and use auto must pass their choice: %+v %+v", got[3], got[4])
	}
	if got[5].Method != http.MethodDelete || got[5].Query != "id=Work+Chrome" {
		t.Fatalf("rm=%+v", got[5])
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

func TestDoctorShowsTheResolvedSigninAndHowToChangeIt(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the browser fixture uses macOS locations")
	}
	home := t.TempDir()
	chrome := filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	for path, content := range map[string]string{
		filepath.Join(chrome, "Local State"):          `{"profile":{"last_used":"Profile 2","info_cache":{"Profile 2":{"name":"Work"}}}}`,
		filepath.Join(chrome, "Profile 2", "Cookies"): "",
		filepath.Join(home, "Library", "Preferences", "com.apple.LaunchServices", "com.apple.launchservices.secure.plist"): `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>LSHandlers</key><array><dict><key>LSHandlerURLScheme</key><string>https</string><key>LSHandlerRoleAll</key><string>com.google.chrome</string></dict></array></dict></plist>`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("AGENTHAIL_COOKIE_BRIDGE", "")
	app, reg := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	if _, err := reg.BindSignin(signin.Claude, signin.RefOff); err != nil {
		t.Fatal(err)
	}
	app.Surfaces = []SurfaceEntry{{Name: "claude", Surface: surfaces.NewClaude(reg, home)}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor"}) })
	if err != nil {
		t.Fatalf("doctor failed: err=%v output=%s", err, output)
	}
	for _, want := range []string{
		`[sign-in] notion: Google Chrome "Profile 2" (Work), automatic (system default browser)`,
		"change: agenthail signin use notion <profile|auto|none>, or Settings > Browser Sign-in",
		"[sign-in] claude: off, reads no cookies",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output=%q missing %q", output, want)
		}
	}
	if strings.Contains(output, "not set up") {
		t.Fatalf("doctor still says sign-in needs setup: %q", output)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	var report struct {
		Signin []signin.Status `json:"signin"`
	}
	if err != nil || json.Unmarshal([]byte(output), &report) != nil || len(report.Signin) != 2 || report.Signin[0].Mode != signin.Automatic || report.Signin[0].Profile != "Profile 2" || report.Signin[1].Mode != signin.Off {
		t.Fatalf("err=%v output=%s", err, output)
	}
}
