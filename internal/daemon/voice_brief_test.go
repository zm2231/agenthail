package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/voice"
)

func TestAVoiceBriefIsReadFromTheSessionsJournalAndNamesWhatItCouldNotRead(t *testing.T) {
	d, reg, _, from, _ := daemonFixture(t)
	journal := func(key, payload string) {
		t.Helper()
		if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: key, Payload: []byte(payload), ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
			t.Fatal(err)
		}
	}
	journal("q1", `{"itemId":"q1","version":1,"op":"upsert","kind":"message","role":"user","body":"Why does the build fail after reconnecting?"}`)
	journal("a1", `{"itemId":"a1","version":1,"op":"upsert","kind":"text","role":"assistant","body":"The reconnect drops the pending build state.","final":true}`)

	src := voiceBriefs{d: d}.BriefSource(context.Background(), &from)
	if len(src.Exchanges) != 1 || src.Exchanges[0].User != "Why does the build fail after reconnecting?" || src.Exchanges[0].Assistant != "The reconnect drops the pending build state." || src.JournalSeq == 0 {
		t.Fatalf("source=%+v", src)
	}
	text, info := voice.RenderBrief(src)
	if !strings.Contains(text, "The reconnect drops the pending build state.") || info.Exchanges != 1 {
		t.Fatalf("brief=%s info=%+v", text, info)
	}
	if src.Goal != nil || src.Compactions != nil || len(src.Missing) != 2 {
		t.Fatalf("the fixture reports no goal or compactions, so both must be named missing: %q", src.Missing)
	}
	for _, missing := range src.Missing {
		if !strings.Contains(text, missing) {
			t.Fatalf("brief does not name %q:\n%s", missing, text)
		}
	}
}

func TestAVoiceBriefReadThatOutlastsItsDeadlineIsReportedMissingWithoutWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	_, err := within(ctx, func() (int, error) {
		<-release
		return 1, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "not read in time") {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waited %s for a blocked read", elapsed)
	}
}

func TestVoiceTurnOutcomesAreReadFromTheJournalNewestFirst(t *testing.T) {
	d, reg, _, from, _ := daemonFixture(t)
	journal := func(key, payload string) {
		t.Helper()
		if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "text", ProviderKey: key, Payload: []byte(payload), ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
			t.Fatal(err)
		}
	}
	journal("a1", `{"itemId":"a1","version":1,"op":"upsert","kind":"text","role":"assistant","turnId":"t1","body":"Working on it."}`)
	journal("a1", `{"itemId":"a1","version":2,"op":"upsert","kind":"text","role":"assistant","turnId":"t1","body":"All 40 tests pass.","final":true}`)
	journal("d1", `{"itemId":"d1","version":1,"op":"upsert","kind":"done","turnId":"t1"}`)
	journal("a2", `{"itemId":"a2","version":1,"op":"upsert","kind":"text","role":"assistant","turnId":"t2","body":"Still reading the logs."}`)
	journal("d3", `{"itemId":"d3","version":1,"op":"upsert","kind":"done","turnId":"t3","status":"failed","body":"out of usage"}`)

	outcomes, err := voiceBriefs{d: d}.TurnOutcomes(context.Background(), &from, []string{"t1", "t2", "t3", "t4"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]voice.TurnOutcome{
		"t1": {Found: true, Done: true, Reply: "All 40 tests pass."},
		"t2": {Found: true, Reply: "Still reading the logs."},
		"t3": {Found: true, Done: true, Failed: "out of usage"},
		"t4": {},
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("outcomes=%+v", outcomes)
	}
}
