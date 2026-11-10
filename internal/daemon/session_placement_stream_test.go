package daemon

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestASplicedMemberKeepsEveryOtherPayloadByte(t *testing.T) {
	payload := []byte(`{ "session" : {"status":"idle",  "id":"s1","surface":"claude","futureField":{"b":2,"a":1}},"extra":[1, 2]}`)
	served, err := spliceSessionMember(payload, "placement", "digest", true)
	if err != nil {
		t.Fatal(err)
	}
	inserted := []byte(`,"placement":"digest"`)
	if !bytes.Contains(served, inserted) || !bytes.Equal(bytes.Replace(served, inserted, nil, 1), payload) {
		t.Fatalf("served %s\nfrom    %s", served, payload)
	}
}

func TestASplicedMemberOnAnEmptySessionObject(t *testing.T) {
	served, err := spliceSessionMember([]byte(`{"session":{}}`), "placement", "digest", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(served, []byte(`{"session":{"placement":"digest"}}`)) {
		t.Fatalf("served %s", served)
	}
}

func TestASplicedMemberReplacesOnlyAStoredValue(t *testing.T) {
	payload := []byte(`{"echo":{"id":"s1"},  "session": { "status":"idle","placement" :  "stale", "id":"s1","surface":"claude","future":{"placement":"nested","z":[1, 2]}} }`)
	served, err := spliceSessionMember(payload, "placement", "fresh", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.Replace(payload, []byte(`"stale"`), []byte(`"fresh"`), 1); !bytes.Equal(served, want) {
		t.Fatalf("served %s\nwant   %s", served, want)
	}
}

func TestAnUnsetMemberClearsAStoredValueAndAddsNothingElse(t *testing.T) {
	payload := []byte(`{"session":{"id":"s1","attention":{"kind":"request","reason":"old"}}}`)
	served, err := spliceSessionMember(payload, "attention", nil, false)
	if err != nil || !bytes.Equal(served, []byte(`{"session":{"id":"s1","attention":null}}`)) {
		t.Fatalf("served %s, err %v", served, err)
	}
	bare := []byte(`{"session":{"id":"s1"}}`)
	served, err = spliceSessionMember(bare, "attention", nil, false)
	if err != nil || !bytes.Equal(served, bare) {
		t.Fatalf("served %s, err %v", served, err)
	}
}

func TestASplicedMemberFindsTheSessionMemberNotAnEarlierCopy(t *testing.T) {
	payload := []byte(`{"before":{"id":"s1","surface":"claude","status":"idle"},"session":{"id":"s1","surface":"claude","status":"idle"}}`)
	served, err := spliceSessionMember(payload, "placement", "digest", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(served, []byte(`{"before":{"id":"s1","surface":"claude","status":"idle"},"session":{"id":"s1","surface":"claude","status":"idle","placement":"digest"}}`)) {
		t.Fatalf("served %s", served)
	}
}

func TestAPresentedUpsertLeavesEventsWithoutASessionAlone(t *testing.T) {
	payload := []byte(`{"sessionId":"s1","reason":"unlisted"}`)
	served, err := presentedUpsert(payload, rowPresenter{now: time.Now()})
	if err != nil || !bytes.Equal(served, payload) {
		t.Fatalf("served %s, err %v", served, err)
	}
}

func TestAPresentedUpsertCarriesTheRowsDisplayAndAttention(t *testing.T) {
	now := time.Now()
	waiting := dashboardSession{ID: "s1", Surface: surface.KindClaude, Status: surface.StatusBusy, WaitingOn: &surface.Waiting{Kind: surface.WaitingApproval, ID: "p1", Since: now}}
	replaced := dashboardSession{ID: "old", Surface: surface.KindClaude, Status: surface.StatusBusy, WaitingOn: &surface.Waiting{Kind: surface.WaitingQuestion, ID: "q1", Since: now}}
	remote := dashboardSession{ID: "laptop::old", Surface: surface.KindClaude, Status: surface.StatusBusy, WaitingOn: &surface.Waiting{Kind: surface.WaitingQuestion, ID: "q2", Since: now}}
	presenter := rowPresenter{now: now, leads: map[string]bool{}, replaced: map[string][]registry.CoordinatorTenure{"old": {{}}, "laptop::old": {{}}}}
	for _, test := range []struct {
		row  dashboardSession
		kind string
	}{{waiting, "request"}, {replaced, AttentionReplacedRequest}, {remote, "request"}} {
		payload, err := json.Marshal(map[string]any{"session": test.row})
		if err != nil {
			t.Fatal(err)
		}
		served, err := presentedUpsert(payload, presenter)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(served, &decoded); err != nil {
			t.Fatal(err)
		}
		want := test.row
		presenter.present(&want, localSessionID(want.ID))
		if decoded.Session.Attention == nil || decoded.Session.Attention.Kind != test.kind || *decoded.Session.Attention != *want.Attention {
			t.Errorf("%s: streamed attention %+v, the row presents %+v", test.row.ID, decoded.Session.Attention, want.Attention)
		}
		if decoded.Session.Display == nil || *decoded.Session.Display != *want.Display || decoded.Session.Placement != sessionPlacement(want) {
			t.Errorf("%s: streamed display %+v placement %s, the row presents %+v", test.row.ID, decoded.Session.Display, decoded.Session.Placement, want.Display)
		}
	}
}
