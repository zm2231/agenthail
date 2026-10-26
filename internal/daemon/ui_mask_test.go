package daemon

import (
	"net/http"
	"strings"
	"testing"
)

func TestUIAPIChangeTakesEffectOnTheAskingDevice(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: "secret"}
	var view uiView
	seed := `{"action":"write","text":"version = 1\nbackground@mac = art: tide\ntexture@mac = none\nglass.blur@mac = 12\n"}`
	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac&device=studio", seed, &view); code != http.StatusOK {
		t.Fatalf("seed status=%d", code)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=phone&device=pocket", `{"action":"set","set":["background = art: dawn"],"message":"Set background to Dawn"}`, &view); code != http.StatusOK {
		t.Fatalf("phone set status=%d", code)
	}
	if !strings.Contains(view.Text, "background@mac = art: tide") || view.Change.Message != "Set background to Dawn" {
		t.Fatalf("a phone write touched the Mac lines: %q %+v", view.Text, view.Change)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac&device=studio", `{"action":"set","set":["glass.blur@device:studio = 30"],"message":"Set blur on studio"}`, &view); code != http.StatusOK {
		t.Fatalf("this Mac set status=%d", code)
	}
	if view.Values["glass.blur"] != 30.0 || !strings.Contains(view.Text, "glass.blur@mac = 12") || view.Change.Message != "Set blur on studio" {
		t.Fatalf("this Mac write: blur %v text %q change %+v", view.Values["glass.blur"], view.Text, view.Change)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac&device=studio", `{"action":"set","set":["background = art: caustics"],"message":"Set background to Caustics"}`, &view); code != http.StatusOK {
		t.Fatalf("set status=%d", code)
	}
	if view.Chosen["background"] != "art: caustics" || strings.Contains(view.Text, "background@mac") {
		t.Fatalf("all-devices write is masked on the Mac: chosen %q text %q", view.Chosen["background"], view.Text)
	}
	if view.Change == nil || view.Change.Message != "Set background to Caustics; cleared background@mac" {
		t.Fatalf("change = %+v", view.Change)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac&device=studio", `{"action":"unset","unset":["texture","glass.blur"],"message":"Reset appearance"}`, &view); code != http.StatusOK {
		t.Fatalf("reset status=%d", code)
	}
	if view.Text != "version = 1\nbackground = art: caustics\n" || view.Change.Message != "Reset appearance; cleared texture@mac, glass.blur@mac, glass.blur@device:studio" {
		t.Fatalf("reset: texture %v change %+v text %q", view.Values["texture"], view.Change, view.Text)
	}

	if code := api.do(http.MethodPost, "/api/v1/ui?class=mac&device=studio", `{"action":"undo"}`, &view); code != http.StatusOK {
		t.Fatalf("undo status=%d", code)
	}
	if !strings.Contains(view.Text, "texture@mac = none") || !strings.Contains(view.Text, "glass.blur@device:studio = 30") {
		t.Fatalf("undo did not restore the cleared lines: %q", view.Text)
	}
}
