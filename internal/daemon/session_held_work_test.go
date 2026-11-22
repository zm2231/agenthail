package daemon

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func (h *runningDaemon) heldWorkIn(path, id string) []heldWorkRow {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, path, nil)
	if status != http.StatusOK {
		h.t.Fatalf("%s status=%d body=%s", path, status, payload)
	}
	var decoded struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	for _, session := range decoded.Sessions {
		if session.ID == id {
			return session.HeldWork
		}
	}
	return nil
}

func TestASessionsRowShowsTheWorkItHoldsAndItsNewestLogLine(t *testing.T) {
	home := codextest.Home(t)
	codextest.InstallLifecycle(t, home, func(string, map[string]any) map[string]any { return nil })
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	var d *Daemon
	h, _ := runDaemonWith(t, store, func(daemon *Daemon) { d = daemon }, codexsurface.NewCodex(unreachableDesktop))
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "surface": "codex", "message": "Clean the build", "cwd": t.TempDir()})
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	id := created.Session.ID
	const key = "repo:github.com/acme/app"
	work := func(req ledger.WorkRequest) ledger.WorkResult {
		t.Helper()
		req.ProjectKey, req.SessionID = key, id
		result, err := d.ledgerStore().Work(req, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	item := work(ledger.WorkRequest{Action: ledger.WorkAdd, Text: "clean the build"}).Item.ID
	work(ledger.WorkRequest{Action: ledger.WorkClaim, ItemID: item})
	work(ledger.WorkRequest{Action: ledger.WorkLog, ItemID: item, Text: "dropped the stale cache step", LogType: "decision"})

	for _, path := range []string{"/api/v1/snapshot", "/api/v1/snapshot?scope=all"} {
		testkit.Eventually(t, "the row on "+path+" to show the held item", 10*time.Second, func() bool {
			held := h.heldWorkIn(path, id)
			return len(held) == 1 && held[0].ItemID == item && held[0].State == "in_progress" && held[0].ProjectKey == key &&
				held[0].Log != nil && held[0].Log.Type == "decision" && held[0].Log.Text == "dropped the stale cache step"
		})
	}
	work(ledger.WorkRequest{Action: ledger.WorkClose, ItemID: item, Reason: "obsolete"})
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/snapshot?scope=all"} {
		testkit.Eventually(t, "the row on "+path+" to drop the closed item", 10*time.Second, func() bool {
			return len(h.heldWorkIn(path, id)) == 0
		})
	}
}
