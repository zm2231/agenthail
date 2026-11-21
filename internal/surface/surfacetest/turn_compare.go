package surfacetest

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func WantTurn(t *testing.T, label string, got, want *surface.TurnMetadata) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s turn:\n got %s\nwant %s", label, TurnJSON(t, got), TurnJSON(t, want))
	}
}

func TurnJSON(t *testing.T, turn *surface.TurnMetadata) string {
	t.Helper()
	encoded, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
