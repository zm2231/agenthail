package daemon

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAPresencePassBuildsOnTheProjectionAnotherWriterStored(t *testing.T) {
	d, r, _, _ := projectsFixture(t)
	d.publishCatalogPresence()
	prior, _, err := r.CatalogProjectionFingerprint("from")
	if err != nil {
		t.Fatal(err)
	}
	row := storedProjection(t, r, "from")
	row.ConfiguredModel = "model-b"
	next, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.UpdateCatalogSessionProjection("from", prior, string(next)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions("from", "follow up", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	d.publishCatalogPresence()
	if row := storedProjection(t, r, "from"); row.QueueCount != 1 || row.ConfiguredModel != "model-b" {
		t.Fatalf("queueCount=%d configuredModel=%q", row.QueueCount, row.ConfiguredModel)
	}
}

func TestASettledPresenceRowStillPublishesAnArchiveAndASessionChange(t *testing.T) {
	d, r, fake, _ := projectsFixture(t)
	d.publishCatalogPresence()
	d.publishCatalogPresence()
	if row := d.presence.rows["from"]; !row.isSettled {
		t.Fatalf("an unchanged row is not settled after two passes: %+v", row)
	}
	if response := runDashboardAction(t, d, `{"action":"session-archive","sessionId":"from"}`); response.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", response.Code, response.Body)
	}
	if row := storedProjection(t, r, "from"); !row.Archived {
		t.Fatalf("a settled row was not archived: %+v", row)
	}
	d.publishCatalogPresence()
	d.publishCatalogPresence()
	if row := storedProjection(t, r, "from"); row.Current {
		t.Fatalf("an idle row is current: %+v", row)
	}
	busy := fake.sessions["from"]
	busy.Status = surface.StatusBusy
	if err := r.RegisterSession(busy); err != nil {
		t.Fatal(err)
	}
	d.publishCatalogPresence()
	if row := storedProjection(t, r, "from"); !row.Current {
		t.Fatalf("a settled row whose session became busy was not made current: %+v", row)
	}
}
