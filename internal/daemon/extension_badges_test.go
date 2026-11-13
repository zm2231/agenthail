package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/poll"
)

func badgeFixture(t *testing.T) (*mountFixture, func(token string, body string) *httptest.ResponseRecorder) {
	t.Helper()
	f := startMountFixture(t)
	f.d.extensions.mu.Lock()
	f.d.extensions.loaded["fixture"] = &loadedExtension{ext: extension.Extension{ID: "fixture", Manifest: extension.Manifest{Label: "Fixture", Mounts: []extension.Mount{{Name: "app", Label: "App"}}}}, host: extensionHostSummary{State: "loaded"}}
	f.d.extensions.tokens["ext-token"] = "fixture"
	f.d.extensions.mu.Unlock()
	return f, func(token string, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, extensionBadgePath, strings.NewReader(body))
		request.RemoteAddr = "127.0.0.1:5555"
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		f.handler.ServeHTTP(recorder, request)
		return recorder
	}
}

func fixtureView(t *testing.T, d *Daemon) extensionView {
	t.Helper()
	for _, ext := range d.extensionsView().Extensions {
		if ext.ID == "fixture" {
			return ext
		}
	}
	t.Fatal("no fixture in the view")
	return extensionView{}
}

func TestAnExtensionSetsItsOwnBadgesAndOnlyThose(t *testing.T) {
	f, badge := badgeFixture(t)
	for body, want := range map[string]int{
		`{"value": 3}`:                     http.StatusOK,
		`{"mount": "app", "value": "new"}`: http.StatusOK,
		`{"mount": "other", "value": "x"}`: http.StatusNotFound,
		`{"value": "much too long"}`:       http.StatusBadRequest,
		`{"value": -1}`:                    http.StatusBadRequest,
		`{"value": 1.5}`:                   http.StatusBadRequest,
		`{"value": {"count": 1}}`:          http.StatusBadRequest,
	} {
		if got := badge("ext-token", body); got.Code != want {
			t.Errorf("%s answered %d %s, want %d", body, got.Code, got.Body, want)
		}
	}
	if got := badge("someone-else", `{"value": 1}`); got.Code != http.StatusUnauthorized {
		t.Fatalf("another token set a badge with %d", got.Code)
	}
	view := fixtureView(t, f.d)
	if view.Badge != "3" {
		t.Fatalf("the extension's badge is %q", view.Badge)
	}
	encoded, _ := json.Marshal(view)
	if !strings.Contains(string(encoded), `"badge":"3"`) {
		t.Fatalf("the view JSON is %s", encoded)
	}
	if got := badge("ext-token", `{"value": 0}`); got.Code != http.StatusOK {
		t.Fatal(got.Body)
	}
	if got := badge("ext-token", `{"value": 12345}`); !strings.Contains(got.Body.String(), "9999+") {
		t.Fatalf("a large count answered %s", got.Body)
	}
	if got := badge("ext-token", `{"value": null}`); got.Code != http.StatusOK || fixtureView(t, f.d).Badge != "" {
		t.Fatalf("clearing the badge left %q", fixtureView(t, f.d).Badge)
	}
}

func TestBadgeChangesReachClientsAtMostOncePerIntervalAndTheLastOneWins(t *testing.T) {
	previous := extensionBadgeInterval
	extensionBadgeInterval = 300 * time.Millisecond
	t.Cleanup(func() { extensionBadgeInterval = previous })
	f, badge := badgeFixture(t)
	cursor := f.d.events.cursor()
	for _, value := range []string{"1", "2", "3", "4"} {
		if got := badge("ext-token", `{"value": `+value+`}`); got.Code != http.StatusOK {
			t.Fatal(got.Body)
		}
	}
	count := func() int {
		backlog, follower, _ := f.d.events.subscribe(cursor)
		follower.Cancel()
		n := 0
		for _, event := range backlog {
			if event.Type == extensionBadgeEvent && event.EntityID == "fixture" {
				n++
			}
		}
		return n
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) { return count() == 2, nil }); err != nil {
		t.Fatalf("four quick changes published %d events, want the first and one trailing", count())
	}
	time.Sleep(2 * extensionBadgeInterval)
	if n := count(); n != 2 {
		t.Fatalf("published %d events after settling", n)
	}
	if got := fixtureView(t, f.d).Badge; got != "4" {
		t.Fatalf("the badge settled on %q", got)
	}
}

const badgeFixtureServer = `
export default function (ah: any) {
  ah.action('badge', (input: { value: unknown }) => ah.badge(input.value));
  ah.action('bad-badge', async () => {
    try { await ah.badge('far too long a badge'); return 'set'; } catch (error: any) { return error.code; }
  });
}
`

func TestAnExtensionSetsItsBadgeThroughAh(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"server.ts": badgeFixtureServer})
	status, reply := f.action(t, "badge", map[string]any{"value": 7})
	if status != http.StatusOK || string(reply["result"]) != `"7"` {
		t.Fatalf("ah.badge answered %d %s", status, reply["result"])
	}
	if got := fixtureView(t, f.d).Badge; got != "7" {
		t.Fatalf("the view's badge is %q", got)
	}
	if _, reply := f.action(t, "bad-badge", nil); string(reply["result"]) != `"invalid"` {
		t.Fatalf("an invalid badge answered %s", reply["result"])
	}
}
