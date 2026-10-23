package daemon

import (
	"context"
	"errors"
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
