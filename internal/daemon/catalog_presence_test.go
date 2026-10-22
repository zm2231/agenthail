package daemon

import (
	"encoding/json"
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
