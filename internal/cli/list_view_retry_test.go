package cli

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestListRestartsWhenTheCatalogChangesMidRead(t *testing.T) {
	calls := 0
	app := snapshotFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		revision := "r1"
		if calls > 2 {
			revision = "r2"
		}
		q := r.URL.Query()
		if q.Has("sessionId") {
			if q.Get("revision") != revision {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"error":{"code":"catalog_changed","message":"Reload the catalog before continuing."}}`)
				return
			}
			fmt.Fprintf(w, `{"view":{"revision":%q,"scope":"current","counts":{"current":{"families":1,"sessions":2}},"families":[]},"sessions":[{"id":"child","name":"child","display":{"kind":"idle","label":"Ready"}}]}`, revision)
			return
		}
		if calls == 2 {
			revision = "r2"
		}
		fmt.Fprintf(w, `{"view":{"revision":%q,"scope":"current","counts":{"current":{"families":1,"sessions":2}},"families":[{"id":"root","rootId":"root","members":[{"id":"root"},{"id":"child","depth":1}]}]},"sessions":[{"id":"root","name":"root","display":{"kind":"idle","label":"Ready"}}]}`, revision)
	})
	out, err := captureStdout(t, func() error { return app.Run([]string{"list"}) })
	if err != nil || !strings.Contains(out, "1 conversation, 2 sessions") || !strings.Contains(out, "child") {
		t.Fatalf("out=%s err=%v calls=%d", out, err, calls)
	}
}
