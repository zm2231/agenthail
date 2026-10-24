package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestMarshalAPIJSONNormalizesTimestamps(t *testing.T) {
	encoded, err := marshalAPIJSON(map[string]any{
		"timestamp": time.Date(2026, 10, 24, 1, 2, 3, 0, time.FixedZone("offset", -5*60*60)),
		"nested":    map[string]string{"createdAt": "2026-10-24T01:02:03Z"},
		"queuedAt":  "2026-10-24 01:02:03",
		"message":   "2026-10-24T01:02:03Z",
		"ts":        "2026-10-20T13:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2026-10-24T06:02:03.000000000Z", "2026-10-24T01:02:03.000000000Z"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("marshalAPIJSON() = %s, missing %q", encoded, want)
		}
	}
	if !strings.Contains(string(encoded), `"message":"2026-10-24T01:02:03Z"`) {
		t.Fatalf("marshalAPIJSON() changed message: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"queuedAt":"2026-10-24T01:02:03.000000000Z"`) {
		t.Fatalf("marshalAPIJSON() left queuedAt unnormalized: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"ts":"2026-10-20T13:00:00.000000000Z"`) {
		t.Fatalf("marshalAPIJSON() left ts unnormalized: %s", encoded)
	}
}
