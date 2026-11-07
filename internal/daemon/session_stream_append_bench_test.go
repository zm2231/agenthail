package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func BenchmarkStreamingReplyAppends(b *testing.B) {
	const delta = 24
	for _, reply := range []int{4 << 10, 32 << 10, 256 << 10} {
		b.Run(fmt.Sprintf("reply-%dKB", reply>>10), func(b *testing.B) {
			r, err := registry.Open(registrytest.Path(b))
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()
			session := surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
			if err := r.RegisterSession(session); err != nil {
				b.Fatal(err)
			}
			source := &sessionSource{ctx: context.Background(), manager: newTestSourceManager(b, r), session: session, epoch: "bench", appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
			chunk := strings.Repeat("w", delta)
			events := reply / delta
			for turn := 0; b.Loop(); turn++ {
				key := fmt.Sprintf("turn-%d:text", turn)
				for index := 0; index < events; index++ {
					source.append(surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(index + 1), Operation: "append", TurnID: fmt.Sprint(turn), Kind: "text", Role: "assistant", Text: chunk})
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*events), "ns/event")
		})
	}
}

func BenchmarkStreamingReplyWire(b *testing.B) {
	const delta = 24
	for _, reply := range []int{4 << 10, 32 << 10, 256 << 10} {
		b.Run(fmt.Sprintf("reply-%dKB", reply>>10), func(b *testing.B) {
			r, err := registry.Open(registrytest.Path(b))
			if err != nil {
				b.Fatal(err)
			}
			defer r.Close()
			session := surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
			if err := r.RegisterSession(session); err != nil {
				b.Fatal(err)
			}
			source := &sessionSource{ctx: context.Background(), manager: newTestSourceManager(b, r), session: session, epoch: "wire", appendBodies: map[string]*streamedBody{}, followers: map[uint64]chan struct{}{}}
			d := &Daemon{Registry: r}
			chunk := strings.Repeat("w", delta)
			events := reply / delta
			wire := 0
			for turn := 0; b.Loop(); turn++ {
				recorder := httptest.NewRecorder()
				stream := newSessionStreamWriter(recorder, session.ID, d.journalItemReader(session.ID), func(ref string, head, from int) (registry.SessionJournalBodyView, bool, error) {
					return r.SessionJournalBodyView(session.ID, ref, head, from)
				})
				var watermark uint64
				key := fmt.Sprintf("turn-%d:text", turn)
				for index := 0; index < events; index++ {
					source.append(surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(index + 1), Operation: "append", TurnID: fmt.Sprint(turn), Kind: "text", Role: "assistant", Text: chunk})
					if _, err := readSessionJournalAfter(r, session.ID, &watermark, func(entry registry.SessionJournalEntry) bool { return stream.write(entry) == nil }); err != nil {
						b.Fatal(err)
					}
				}
				wire += recorder.Body.Len()
			}
			b.ReportMetric(float64(wire)/float64(b.N*events), "wire-bytes/event")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*events), "ns/event")
		})
	}
}
