package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func fillSourceBodies(source *sessionSource, items int) {
	for index := 0; index < items; index++ {
		source.append(surface.StreamEvent{ProviderKey: fmt.Sprintf("item-%d", index), Kind: "text", Operation: "upsert", Final: true, Text: fmt.Sprintf("%08d", index) + strings.Repeat("x", 4088)})
	}
}

// Item text kept to extend later appends grew with every item a session
// streamed: 16 MiB for 4,096 finished 4 KiB items, twice what the journal
// itself retains.
func TestASourceKeepsNoMoreItemTextThanItsBound(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{ctx: context.Background(), manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "bodies", followers: map[uint64]chan struct{}{}}
	fillSourceBodies(source, 4096)
	if source.appendBodies.bytes > sessionSourceBodyBytes {
		t.Fatalf("source keeps %d bytes of item text, want at most %d", source.appendBodies.bytes, sessionSourceBodyBytes)
	}
}

func TestAnAppendToAnItemWhoseTextTheSourceDroppedExtendsTheJournaledText(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "inline", text: "héllo wörld "},
		{name: "referenced", text: strings.Repeat("grüße ", sessionStreamBodyBytes/4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, reg, fake, from, _ := daemonFixture(t)
			source := &sessionSource{ctx: context.Background(), manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "bodies", followers: map[uint64]chan struct{}{}}
			source.append(surface.StreamEvent{ProviderKey: "late", Kind: "text", Role: "assistant", Operation: "upsert", Text: tc.text})
			fillSourceBodies(source, 2*sessionSourceBodyBytes/4096)
			if source.appendBodies.get("late") != nil {
				t.Fatal("fixture did not push the item's text out of the source")
			}
			source.append(surface.StreamEvent{ProviderKey: "late", Kind: "text", Role: "assistant", Operation: "append", Text: "und weiter ✓"})
			want := tc.text + "und weiter ✓"
			if _, sourceErrors := journalRows(t, reg, from.ID); len(sourceErrors) != 0 {
				t.Fatalf("source errors: %q", sourceErrors)
			}
			entry, found, err := reg.SessionJournalEntryByProviderKey(from.ID, "late")
			if err != nil || !found {
				t.Fatalf("found=%v err=%v", found, err)
			}
			var row sessionJournalPayload
			if err := json.Unmarshal(entry.Payload, &row); err != nil {
				t.Fatal(err)
			}
			got := row.Body
			if row.BodyRef != "" {
				got = string(entry.FullBody)
			}
			if got != want {
				t.Fatalf("journaled text is %d bytes ending %q, want %d bytes ending %q", len(got), got[max(0, len(got)-24):], len(want), want[len(want)-24:])
			}
		})
	}
}
