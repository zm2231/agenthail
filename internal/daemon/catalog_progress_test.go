package daemon

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func dashboardSessionKeys() []string {
	var keys []string
	fields := reflect.TypeFor[dashboardSession]()
	for i := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

func upsertChanging(status surface.SessionStatus, extra map[string]any, changed ...string) registry.CatalogEvent {
	session := map[string]any{"id": "s1", "status": status}
	maps.Copy(session, extra)
	envelope := map[string]any{"session": session}
	if changed != nil {
		envelope["changed"] = changed
	}
	payload, _ := json.Marshal(envelope)
	return registry.CatalogEvent{Type: "session.upserted", EntityID: "s1", Payload: payload}
}

func TestEveryOtherSessionFieldWakesTheDecisionsAndWorkWatchers(t *testing.T) {
	keys := dashboardSessionKeys()
	for key := range catalogProgressFields {
		if !slices.Contains(keys, key) {
			t.Errorf("progress field %q is not a session row field", key)
		}
	}
	for _, key := range keys {
		if tick := catalogEventIdle(upsertChanging(surface.StatusBusy, nil, key)); tick != catalogProgressFields[key] {
			t.Errorf("a busy session changing only %q: idle=%v, want %v", key, tick, catalogProgressFields[key])
		}
	}
	for name, event := range map[string]registry.CatalogEvent{
		"an upsert that does not say what changed": upsertChanging(surface.StatusBusy, nil),
		"an idle session's progress":               upsertChanging(surface.StatusIdle, nil, "activity"),
		"progress while waiting on the person":     upsertChanging(surface.StatusBusy, map[string]any{"waitingOn": map[string]any{"id": "r1"}}, "activity"),
		"progress while asking the person":         upsertChanging(surface.StatusBusy, map[string]any{"ask": map[string]any{"id": "a1"}}, "lastActive"),
		"a queue change":                           {Type: "session.queue", EntityID: "s1", Payload: []byte(`{}`)},
	} {
		if catalogEventIdle(event) {
			t.Errorf("%s was skipped", name)
		}
	}
}

func TestProgressTicksLeaveDecisionsAndTheWorkPageAlone(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.holdWork(t, "ship the importer")
	f.waitOnPrompt(t, "worker")
	start := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	lead := f.surface.sessions["lead"]
	lead.Status, lead.LastActive = surface.StatusBusy, start
	lead.Activity = &surface.Activity{BusySince: start, LastProgressAt: start, Tool: &surface.RunningTool{Name: "Bash", StartedAt: start}}
	f.surface.sessions["lead"] = lead
	f.surface.observations["lead"] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t-lead", Activity: lead.Activity}
	refresh := func() {
		f.d.scanAndRelay(t.Context())
		f.d.discoverCatalog(t.Context())
	}
	testkit.Eventually(t, "the lead busy with progress", 5*time.Second, func() bool {
		refresh()
		row := f.row(t, "lead")
		return row.Status == surface.StatusBusy && row.Activity != nil
	})

	mutators := map[string]func(step int){
		"activity": func(step int) {
			at := start.Add(time.Duration(step) * time.Minute)
			session := f.surface.sessions["lead"]
			session.Activity = &surface.Activity{BusySince: start, LastProgressAt: at, Tool: &surface.RunningTool{Name: "Edit", StartedAt: at}}
			f.surface.sessions["lead"] = session
			f.surface.observations["lead"].Activity = session.Activity
		},
		"lastActive": func(step int) {
			session := f.surface.sessions["lead"]
			session.LastActive = start.Add(time.Duration(step) * time.Minute)
			f.surface.sessions["lead"] = session
		},
	}
	if !slices.Equal(slices.Sorted(maps.Keys(mutators)), slices.Sorted(maps.Keys(catalogProgressFields))) {
		t.Fatalf("every progress field needs a mutator here: mutators %v, fields %v", slices.Sorted(maps.Keys(mutators)), slices.Sorted(maps.Keys(catalogProgressFields)))
	}

	_, latest, err := f.r.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	_, follower, err := f.d.catalog.subscribe(latest)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Cancel()
	revision, board := ownRevision(t, f.d), f.d.ledgerBoardVersion("p-answer")
	if board == "" {
		t.Fatal("the Work page read no board")
	}
	step := 0
	for _, field := range slices.Sorted(maps.Keys(mutators)) {
		step++
		cursor := follower.cursor
		mutators[field](step)
		refresh()
		window, err := f.r.CatalogEventsAfter(cursor, 1000)
		if err != nil {
			t.Fatal(err)
		}
		named := false
		for _, event := range window.Events {
			changed, _ := registry.SessionUpsertChanges(event)
			named = named || event.EntityID == "lead" && slices.Contains(changed, field)
		}
		if !named {
			t.Fatalf("changing the lead's %s published no upsert naming it: %d events", field, len(window.Events))
		}
		if changed, err := follower.changedBeyond(catalogEventIdle); err != nil || changed {
			t.Fatalf("the lead's %s tick woke the watchers (err=%v)", field, err)
		}
		if again := ownRevision(t, f.d); again != revision {
			t.Fatalf("the lead's %s changed the decisions page", field)
		}
		if again := f.d.ledgerBoardVersion("p-answer"); again != board {
			t.Fatalf("the lead's %s changed the Work page", field)
		}
	}

	f.waitOnPrompt(t, "lead")
	if changed, err := follower.changedBeyond(catalogEventIdle); err != nil || !changed {
		t.Fatalf("the lead waiting on the person did not wake the watchers (err=%v)", err)
	}
	if ownRevision(t, f.d) == revision {
		t.Fatal("the lead waiting on the person left the decisions page alone")
	}
}
