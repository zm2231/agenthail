package daemon

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionDisplayKindsMatchTheSharedVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "fixtures", "session-display-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Now   time.Time      `json:"now"`
		Base  map[string]any `json:"base"`
		Cases []struct {
			Name  string         `json:"name"`
			Patch map[string]any `json:"patch"`
			Kind  string         `json:"kind"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, vector := range vectors.Cases {
		fields := maps.Clone(vectors.Base)
		maps.Copy(fields, vector.Patch)
		encoded, _ := json.Marshal(fields)
		var row dashboardSession
		if err := json.Unmarshal(encoded, &row); err != nil {
			t.Fatalf("%s: %v", vector.Name, err)
		}
		if got := sessionDisplay(row, vectors.Now).Kind; got != vector.Kind {
			t.Errorf("%s: daemon says %q, vectors say %q", vector.Name, got, vector.Kind)
		}
		kinds[vector.Kind] = true
	}
	for _, kind := range []string{"unavailable", "waiting", "working", "limited", "finishing", "queued", "offline", "idle"} {
		if !kinds[kind] {
			t.Errorf("no vector shows the %q kind", kind)
		}
	}
}
