package daemon

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectNowVectorsUseTheDaemonsRowShape(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "fixtures", "project-now-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Base  map[string]any `json:"base"`
		Cases []struct {
			Name     string           `json:"name"`
			Sessions []map[string]any `json:"sessions"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors.Cases {
		for _, patch := range vector.Sessions {
			fields := maps.Clone(vectors.Base)
			maps.Copy(fields, patch)
			encoded, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.DisallowUnknownFields()
			var row dashboardSession
			if err := decoder.Decode(&row); err != nil {
				t.Errorf("%s: %s isn't a row the daemon sends: %v", vector.Name, fields["id"], err)
			}
		}
	}
}
