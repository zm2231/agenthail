package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/uiconf"
)

func uiEvents(d *Daemon) []string {
	backlog, _, _, cancel := d.events.subscribe(0)
	defer cancel()
	var revs []string
	for _, event := range backlog {
		if event.Type == "ui.changed" {
			revs = append(revs, string(event.Data))
		}
	}
	return revs
}

func TestUIAPIReadsChangesAndUndoes(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: "secret"}

	var view uiView
	if code := api.do(http.MethodGet, "/api/v1/ui?class=mac", "", &view); code != http.StatusOK {
		t.Fatalf("get status=%d", code)
	}
	if view.Values["theme"] != "system" || view.Values["background"] != "none" || view.Values["background.light"] != "none" || view.Chosen["background"] != "none" || len(view.Overrides) != 0 || len(view.Problems) != 0 {
		t.Fatalf("initial view = %+v", view)
	}
	first := view.Rev

	var refused signinAPIError
	if code := api.do(http.MethodPost, "/api/v1/ui", `{"action":"set","set":["theme = dark","glass.blur = lots"]}`, &refused); code != http.StatusUnprocessableEntity || !strings.Contains(refused.Error.Message, "glass.blur") {
		t.Fatalf("bad set: status=%d %+v", code, refused)
	}

	var problems struct {
		Error    struct{ Code string } `json:"error"`
		Problems []uiconf.Problem      `json:"problems"`
	}
	if code := api.do(http.MethodPost, "/api/v1/ui", `{"action":"write","text":"version = 1\ntheme = sepia\n"}`, &problems); code != http.StatusUnprocessableEntity || problems.Error.Code != "ui_problems" || len(problems.Problems) != 1 || problems.Problems[0].Line != 2 {
		t.Fatalf("bad write: status=%d %+v", code, problems)
	}
	var unchanged uiView
	if api.do(http.MethodGet, "/api/v1/ui?class=mac", "", &unchanged); unchanged.Rev != first || strings.Contains(unchanged.Text, "sepia") {
		t.Fatalf("refused write changed ui.conf: rev %s -> %s", first, unchanged.Rev)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=phone", `{"action":"set","set":["theme = dark","texture@phone = grain"],"message":"Dark phone with grain"}`, &view); code != http.StatusOK {
		t.Fatalf("set status=%d", code)
	}
	if view.Values["theme"] != "dark" || view.Values["texture"] != "grain" || view.Change == nil || view.Change.Author != uiconf.Person || view.Change.Message != "Dark phone with grain" {
		t.Fatalf("after set = %+v change=%+v", view.Values, view.Change)
	}
	if view.Overrides["texture"] != "phone" || view.Chosen["texture"] != "grain" {
		t.Fatalf("phone overrides = %v chosen texture %q", view.Overrides, view.Chosen["texture"])
	}
	var mac uiView
	if code := api.do(http.MethodGet, "/api/v1/ui?class=mac", "", &mac); code != http.StatusOK || mac.Values["texture"] != "none" || mac.Overrides["texture"] != "" {
		t.Fatalf("mac texture = %v overrides %v (status %d)", mac.Values["texture"], mac.Overrides, code)
	}
	view = mac
	if events := uiEvents(d); len(events) != 1 || !strings.Contains(events[0], view.Rev) {
		t.Fatalf("ui.changed events = %v, want one for %s", events, view.Rev)
	}

	var log struct {
		Entries []uiconf.Entry `json:"entries"`
	}
	if code := api.do(http.MethodGet, "/api/v1/ui/log", "", &log); code != http.StatusOK || len(log.Entries) != 2 {
		t.Fatalf("log status=%d entries=%+v", code, log.Entries)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac", `{"action":"undo"}`, &view); code != http.StatusOK {
		t.Fatalf("undo status=%d", code)
	}
	if view.Values["theme"] != "system" || view.Change == nil || view.Change.Message != "Undo Dark phone with grain" || view.Rev == first {
		t.Fatalf("after undo = %+v change=%+v", view.Values, view.Change)
	}
}

func TestUIAPIScopesAndBackgroundPaths(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	pairing, err := r.CreateDevicePairing("Phone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	reader := signinAPIClient{t: t, handler: handler, token: token}
	if code := reader.do(http.MethodGet, "/api/v1/ui?class=phone", "", nil); code != http.StatusOK {
		t.Fatalf("read-scope get status=%d", code)
	}
	if code := reader.do(http.MethodPost, "/api/v1/ui", `{"action":"set","set":["theme = dark"]}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("read-scope post status=%d, want 401", code)
	}
	if code := reader.do(http.MethodPost, "/api/v1/ui/background?name=wall.png", "png bytes", nil); code != http.StatusUnauthorized {
		t.Fatalf("read-scope upload status=%d, want 401", code)
	}
	owner := signinAPIClient{t: t, handler: handler, token: "secret"}
	for _, path := range []string{"ui.conf", "../registry.db", "backgrounds/../ui.conf", "art/../ui.conf"} {
		if code := owner.do(http.MethodGet, "/api/v1/ui/background?path="+path, "", nil); code != http.StatusBadRequest {
			t.Errorf("background %q status=%d, want 400", path, code)
		}
	}

	var uploaded struct {
		Path string `json:"path"`
	}
	if code := owner.do(http.MethodPost, "/api/v1/ui/background?name=wall.PNG", "png bytes", &uploaded); code != http.StatusOK || !strings.HasPrefix(uploaded.Path, "backgrounds/") || !strings.HasSuffix(uploaded.Path, ".png") {
		t.Fatalf("upload status=%d path=%q", code, uploaded.Path)
	}
	if code := owner.do(http.MethodPost, "/api/v1/ui?class=mac", `{"action":"set","set":["background = image: `+uploaded.Path+`"]}`, nil); code != http.StatusOK {
		t.Fatalf("setting the uploaded image status=%d", code)
	}
	served := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ui/background?path="+uploaded.Path, nil)
	request.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(served, request)
	if served.Code != http.StatusOK || served.Body.String() != "png bytes" {
		t.Fatalf("uploaded image served %d %q", served.Code, served.Body.String())
	}
	var refused signinAPIError
	if code := owner.do(http.MethodPost, "/api/v1/ui/background?name=notes.txt", "text", &refused); code != http.StatusUnprocessableEntity || refused.Error.Code != "background_refused" {
		t.Fatalf("non-image upload status=%d %+v", code, refused)
	}
	for _, name := range []string{"notes.txt", "wall.png"} {
		body := &countingReader{}
		oversized := httptest.NewRequest(http.MethodPost, "/api/v1/ui/background?name="+name, body)
		oversized.ContentLength = 40 << 20
		oversized.Header.Set("Authorization", "Bearer secret")
		refusal := httptest.NewRecorder()
		handler.ServeHTTP(refusal, oversized)
		if refusal.Code != http.StatusUnprocessableEntity || body.read != 0 {
			t.Fatalf("40 MB %s upload status=%d after reading %d bytes, want a refusal before reading", name, refusal.Code, body.read)
		}
	}

	var presets struct {
		Presets []uiconf.Choice `json:"presets"`
	}
	if code := reader.do(http.MethodGet, "/api/v1/ui/presets", "", &presets); code != http.StatusOK || len(presets.Presets) != len(uiconf.ArtNames)+len(uiconf.PresetNames) || presets.Presets[0] != (uiconf.Choice{Value: "art: tide", Name: "Tide", Dark: "image: art/tide-dark.jpg", Light: "image: art/tide-light.jpg"}) {
		t.Fatalf("presets status=%d %+v", code, presets.Presets)
	}

	art := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/ui/background?path=art/tide-dark.jpg", nil)
	request.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(art, request)
	if art.Code != http.StatusOK || art.Header().Get("Content-Type") != "image/jpeg" || art.Header().Get("ETag") == "" || strings.Contains(art.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("built-in picture: %d %v", art.Code, art.Header())
	}
	again := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/ui/background?path=art/tide-dark.jpg", nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("If-None-Match", art.Header().Get("ETag"))
	handler.ServeHTTP(again, request)
	if again.Code != http.StatusNotModified {
		t.Fatalf("an unchanged built-in picture was sent again: %d", again.Code)
	}
}

func TestWatchUICommitsHandEditsAndAnnouncesThem(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	store, err := d.ui.open()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.watchUI(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	testkit.Eventually(t, "the condition", 10*time.Second, func() bool {
		d.ui.mu.Lock()
		defer d.ui.mu.Unlock()
		return d.ui.published != ""
	})
	if err := os.WriteFile(store.Path(), []byte("version = 1\ntheme = light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the condition", 10*time.Second, func() bool { return len(uiEvents(d)) == 1 })
	entries, err := store.Log(1)
	if err != nil || len(entries) != 1 || entries[0].Author != uiconf.Person || entries[0].Message != "Edited by hand" {
		t.Fatalf("latest commit = %+v, %v", entries, err)
	}
}

type countingReader struct{ read int }

func (c *countingReader) Read(p []byte) (int, error) {
	c.read += len(p)
	return len(p), nil
}
