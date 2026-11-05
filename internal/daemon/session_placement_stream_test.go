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

func TestWithPlacementReplacesOnlyAStoredPlacementValue(t *testing.T) {
	payload := []byte(`{"echo":{"id":"s1"},  "session": { "status":"idle","placement" :  "stale", "id":"s1","surface":"claude","future":{"placement":"nested","z":[1, 2]}} }`)
	served, err := withPlacement(payload)
	if err != nil {
		t.Fatal(err)
	}
	fresh := `"` + sessionPlacement(dashboardSession{ID: "s1", Surface: surface.KindClaude, Status: surface.StatusIdle}) + `"`
	if want := bytes.Replace(payload, []byte(`"stale"`), []byte(fresh), 1); !bytes.Equal(served, want) {
		t.Fatalf("served %s\nwant   %s", served, want)
	}
}

func TestWithPlacementFindsTheSessionMemberNotAnEarlierCopy(t *testing.T) {
	payload := []byte(`{"before":{"id":"s1","surface":"claude","status":"idle"},"session":{"id":"s1","surface":"claude","status":"idle"}}`)
	served, err := withPlacement(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(served, []byte(`{"before":{"id":"s1","surface":"claude","status":"idle"},"session":{"id":"s1","surface":"claude","status":"idle","placement":"`)) {
		t.Fatalf("served %s", served)
	}
}

func TestWithPlacementLeavesEventsWithoutASessionAlone(t *testing.T) {
	payload := []byte(`{"sessionId":"s1","reason":"unlisted"}`)
	served, err := withPlacement(payload)
	if err != nil || !bytes.Equal(served, payload) {
		t.Fatalf("served %s, err %v", served, err)
	}
}
