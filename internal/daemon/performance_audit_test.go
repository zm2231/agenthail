//go:build perfaudit

package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAuditSourceCacheRetention(t *testing.T) {
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session := surface.Session{ID: "audit", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop"}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	s := &sessionSource{manager: newSessionSourceManager(r), session: session, epoch: "audit", ctx: context.Background(), appendBodies: map[string]*streamedBody{}, appendCursors: map[string]uint64{}, followers: map[uint64]chan struct{}{}}
	for i := 0; i < 4096; i++ {
		s.append(surface.StreamEvent{ProviderKey: fmt.Sprintf("item-%d", i), Kind: "text", Operation: "upsert", Final: true, Text: fmt.Sprintf("%08d", i) + strings.Repeat("x", 4088), Cursor: uint64(i + 1)})
	}
	bytes := 0
	for _, body := range s.appendBodies {
		bytes += len(body.text)
	}
	_, retained, err := r.SessionJournalEntryByProviderKey(session.ID, "item-0")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("events=4096 cache_keys=%d cache_body_bytes=%d cursor_keys=%d journal_byte_budget=%d first_item_retained=%v", len(s.appendBodies), bytes, len(s.appendCursors), sessionJournalRetentionBytes, retained)
	if retained || len(s.appendBodies) != 4096 || bytes <= sessionJournalRetentionBytes {
		t.Fatal("fixture did not reproduce cache growth beyond journal retention")
	}
}

type auditSlowSurface struct {
	*daemonSurface
	fastAt time.Time
}

func (s *auditSlowSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	if session.ID == "from" {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
			return nil, errors.New("synthetic slow provider")
		}
	}
	s.fastAt = time.Now()
	return &surface.TurnObservation{Status: surface.StatusBusy}, nil
}

func TestAuditObservationHeadOfLine(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	slow := &auditSlowSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{slow}
	for _, id := range []string{"from", "to"} {
		if err := r.QueueMessage(id, "synthetic pending work"); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	d.scanAndRelay(t.Context())
	if slow.fastAt.IsZero() {
		t.Fatal("fast session was not observed")
	}
	wait := slow.fastAt.Sub(started)
	t.Logf("slow_provider_delay=250ms unrelated_session_first_observed_after=%s", wait)
	if wait < 250*time.Millisecond {
		t.Fatal("fixture did not reproduce serial observation")
	}
}

func BenchmarkAuditCatalogPublish(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("sessions_%d", count), func(b *testing.B) {
			r, err := registry.Open(filepath.Join(b.TempDir(), "registry.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { r.Close() })
			fake := &daemonSurface{}
			d := newTestDaemon(b, r, []surface.Surface{fake})
			sessions := make([]surface.Session, count)
			for i := range sessions {
				sessions[i] = surface.Session{ID: fmt.Sprintf("audit-%d", i), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop", Status: surface.StatusOffline}
				if err := r.RecordObservedSession(sessions[i]); err != nil {
					b.Fatal(err)
				}
			}
			pass := func() {
				for _, session := range sessions {
					if !d.publishCatalogSession(context.Background(), fake, session, nil, projectresolve.CatalogIdentity{}, "", 0, nil, false, dashconfig.Config{}, catalogViewState{}, time.Now(), nil, nil) {
						b.Fatal("catalog publication failed")
					}
				}
			}
			pass()
			_, before, err := r.CatalogState()
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pass()
			}
			b.StopTimer()
			_, after, err := r.CatalogState()
			if err != nil || after != before {
				b.Fatalf("unchanged pass emitted catalog events: before=%d after=%d err=%v", before, after, err)
			}
		})
	}
}
