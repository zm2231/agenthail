package daemon

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAReplyStreamedPastTheInlineBodyReadsBackWhole(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	session := surface.Session{ID: "long-reply", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	source := &sessionSource{manager: newTestSourceManager(t, r), session: session, epoch: "long", appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
	const key = "turn-1:text"
	var want strings.Builder
	for index := 0; want.Len() < 3*sessionStreamBodyBytes; index++ {
		delta := fmt.Sprintf("delta %d é ", index)
		want.WriteString(delta)
		source.append(surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(index + 1), Operation: "append", TurnID: "turn-1", Kind: "text", Role: "assistant", Text: delta})
	}
	check := func(label string) {
		t.Helper()
		entry, found, err := r.SessionJournalEntryByProviderKey(session.ID, key)
		if err != nil || !found || entry.BodyRef == "" {
			t.Fatalf("%s: entry found=%v ref=%q err=%v", label, found, entry.BodyRef, err)
		}
		if string(entry.FullBody) != want.String() {
			t.Fatalf("%s: stored body is %d bytes, want the %d streamed", label, len(entry.FullBody), want.Len())
		}
		start := sessionStreamBodyBytes + 3
		part, total, err := r.SessionJournalBody(session.ID, entry.BodyRef, start, start+5000)
		if err != nil || total != want.Len() || !strings.HasPrefix(want.String()[start:], string(part)) || len(part) < 4990 {
			t.Fatalf("%s: body range total=%d len=%d err=%v", label, total, len(part), err)
		}
	}
	check("while streaming")
	source.append(surface.StreamEvent{ID: key, ProviderKey: key, Version: 1 << 20, Operation: "upsert", Final: true, TurnID: "turn-1", Kind: "text", Role: "assistant", Text: want.String()})
	check("after the final upsert")
}
