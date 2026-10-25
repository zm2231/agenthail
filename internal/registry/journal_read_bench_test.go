package registry

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func BenchmarkJournalReads(b *testing.B) {
	r, err := Open(filepath.Join(b.TempDir(), "registry.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	if err := r.RegisterSession(surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
		b.Fatal(err)
	}
	entries := make([]SessionJournalEntry, 2048)
	for i := range entries {
		entries[i] = SessionJournalEntry{Kind: "text", ProviderKey: fmt.Sprint(i), Payload: []byte(`{"kind":"text","body":"retained transcript"}`)}
	}
	if _, err := r.ReplaceSessionJournal("s", entries, SessionJournalRetention{Count: 2048, Bytes: 8 << 20}, 1); err != nil {
		b.Fatal(err)
	}
	for _, before := range []uint64{0, 400} {
		b.Run(fmt.Sprintf("page-%d", before), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := r.ReadSessionJournalPage("s", before, 200); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("caught-up-stream", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := r.SessionJournalAfter("s", 2048, 256); err != nil {
				b.Fatal(err)
			}
		}
	})
}
