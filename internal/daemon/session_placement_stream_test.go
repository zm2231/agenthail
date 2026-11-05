package daemon

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestWithPlacementKeepsEveryOtherPayloadByte(t *testing.T) {
	payload := []byte(`{ "session" : {"status":"idle",  "id":"s1","surface":"claude","futureField":{"b":2,"a":1}},"extra":[1, 2]}`)
	served, err := withPlacement(payload)
	if err != nil {
		t.Fatal(err)
	}
	row := dashboardSession{ID: "s1", Surface: surface.KindClaude, Status: surface.StatusIdle}
	inserted := []byte(`,"placement":"` + sessionPlacement(row) + `"`)
	if !bytes.Contains(served, inserted) || !bytes.Equal(bytes.Replace(served, inserted, nil, 1), payload) {
		t.Fatalf("served %s\nfrom    %s", served, payload)
	}
}

func TestWithPlacementOnAnEmptySessionObject(t *testing.T) {
	served, err := withPlacement([]byte(`{"session":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(served) || !bytes.HasPrefix(served, []byte(`{"session":{"placement":"`)) {
		t.Fatalf("served %s", served)
	}
}

func TestWithPlacementReplacesAStoredPlacement(t *testing.T) {
	served, err := withPlacement([]byte(`{"session":{"id":"s1","surface":"claude","status":"idle","placement":"stale"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Session dashboardSession `json:"session"`
	}
	if err := json.Unmarshal(served, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Session.Placement == "stale" || decoded.Session.Placement != sessionPlacement(dashboardSession{ID: "s1", Surface: surface.KindClaude, Status: surface.StatusIdle}) {
		t.Fatalf("placement=%q", decoded.Session.Placement)
	}
}

func TestWithPlacementLeavesEventsWithoutASessionAlone(t *testing.T) {
	payload := []byte(`{"sessionId":"s1","reason":"unlisted"}`)
	served, err := withPlacement(payload)
	if err != nil || !bytes.Equal(served, payload) {
		t.Fatalf("served %s, err %v", served, err)
	}
}
