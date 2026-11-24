package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRotatingTheSigningKeyWakesCatalogFollowers(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	_, follower, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Cancel()
	if _, err := follower.read(10000); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	d.apiHostSigningKeyHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/hosts/signing-key", strings.NewReader(`{"revoke":false}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	select {
	case <-follower.Wake:
	case <-time.After(time.Second):
		t.Fatal("a follower was not woken for the rotated key")
	}
	events, err := follower.read(10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "host.keys" {
			return
		}
	}
	t.Fatalf("events=%+v, want the rotated key", events)
}
