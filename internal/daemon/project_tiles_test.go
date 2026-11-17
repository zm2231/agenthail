package daemon

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestTheTileOrderIsReadFromTheBaseSnapshotAndReplacedOnlyAgainstItsCurrentRevision(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	token := func(name string, scopes ...string) string {
		pairing, err := r.CreateDevicePairing(name, scopes, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := r.CompleteDevicePairing(pairing.Secret, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	handler := dashboardRouter(d)
	viewer := signinAPIClient{t: t, handler: handler, token: token("Viewer", "read")}
	phone := signinAPIClient{t: t, handler: handler, token: token("Phone", "read", "control")}
	type reply struct {
		TileOrder registry.TileOrder `json:"tileOrder"`
		Error     struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	order := `{"order":["repo:github.com/example/app","other-host::implicit:/srv/notes"],"pinned":["other-host::implicit:/srv/notes"],"revision":0}`

	var refused reply
	if code := viewer.do(http.MethodPut, projectTilesPath, order, &refused); code != http.StatusUnauthorized {
		t.Fatalf("a read-only device changed the order: %d %+v", code, refused)
	}
	var missing reply
	if code := phone.do(http.MethodPut, projectTilesPath, `{"order":[]}`, &missing); code != http.StatusBadRequest {
		t.Fatalf("an order without a revision = %d %+v", code, missing)
	}

	_, follower, _ := d.events.subscribe(snapshotEventCursor(t, handler))
	defer follower.Cancel()
	var written reply
	if code := phone.do(http.MethodPut, projectTilesPath, order, &written); code != http.StatusOK || written.TileOrder.Revision != 1 {
		t.Fatalf("a phone's first order = %d %+v", code, written)
	}
	events, _ := follower.read()
	told := false
	for _, event := range events {
		var data map[string]string
		if event.Type == "state.changed" && json.Unmarshal(event.Data, &data) == nil && data["source"] == "tiles" {
			told = true
		}
	}
	if !told {
		t.Fatalf("writing the order told no client: %+v", events)
	}

	var base struct {
		TileOrder registry.TileOrder `json:"tileOrder"`
	}
	if code := viewer.do(http.MethodGet, "/api/v1/snapshot?sessions=none", "", &base); code != http.StatusOK || base.TileOrder.Revision != 1 || !slices.Equal(base.TileOrder.Order, []string{"repo:github.com/example/app", "other-host::implicit:/srv/notes"}) || !slices.Equal(base.TileOrder.Pinned, []string{"other-host::implicit:/srv/notes"}) {
		t.Fatalf("the base snapshot's order = %d %+v", code, base.TileOrder)
	}

	var stale reply
	if code := phone.do(http.MethodPut, projectTilesPath, `{"order":["repo:github.com/example/app"],"pinned":[],"revision":0}`, &stale); code != http.StatusConflict || stale.Error.Code != "conflict" || stale.TileOrder.Revision != 1 || len(stale.TileOrder.Order) != 2 {
		t.Fatalf("a write against an old revision = %d %+v; want 409 with the current order", code, stale)
	}
	var retried reply
	if code := phone.do(http.MethodPut, projectTilesPath, `{"order":["other-host::implicit:/srv/notes","repo:github.com/example/app"],"pinned":[],"revision":1}`, &retried); code != http.StatusOK || retried.TileOrder.Revision != 2 {
		t.Fatalf("the retry against the current revision = %d %+v", code, retried)
	}
}
