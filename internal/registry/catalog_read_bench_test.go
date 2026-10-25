package registry

import (
	"fmt"
	"path/filepath"
	"testing"
)

func BenchmarkCaughtUpCatalog(b *testing.B) {
	r, err := Open(filepath.Join(b.TempDir(), "registry.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	var latest uint64
	for i := 0; i < catalogEventRetention; i++ {
		event, _, err := r.AppendCatalogEvent(CatalogEvent{DedupeKey: fmt.Sprint(i), Type: "session.upserted", EntityID: "s", Payload: []byte(`{"session":{"id":"s"}}`)})
		if err != nil {
			b.Fatal(err)
		}
		latest = event.Seq
	}
	b.ReportAllocs()
	for b.Loop() {
		window, err := r.CatalogEventsAfter(latest, 256)
		if err != nil || window.Gap || len(window.Events) != 0 {
			b.Fatalf("window=%+v err=%v", window, err)
		}
	}
}
