//go:build perfaudit

package registry

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func auditRegistry(b *testing.B) *Registry {
	b.Helper()
	r, err := Open(filepath.Join(b.TempDir(), "registry.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { r.Close() })
	if err := r.RecordObservedSession(surface.Session{ID: "audit", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop"}); err != nil {
		b.Fatal(err)
	}
	return r
}

func BenchmarkAuditJournalUpdate(b *testing.B) {
	for _, count := range []int{1, 256, 2048} {
		b.Run(fmt.Sprintf("rows_%d", count), func(b *testing.B) {
			r := auditRegistry(b)
			retention := SessionJournalRetention{Count: 2048, Bytes: 8 << 20}
			for i := 0; i < count; i++ {
				_, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "audit", Kind: "text", ProviderKey: fmt.Sprint(i), Payload: []byte(`{"body":"seed"}`)}, retention)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, changed, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "audit", Kind: "text", ProviderKey: "0", Payload: []byte(fmt.Sprintf(`{"body":"value-%d"}`, i))}, retention)
				if err != nil || !changed {
					b.Fatalf("changed=%v err=%v", changed, err)
				}
			}
		})
	}
}

func BenchmarkAuditCatalogUnchanged(b *testing.B) {
	r := auditRegistry(b)
	state := CatalogSessionState{Session: surface.Session{ID: "audit", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: "desktop"}, Project: []byte(`{}`), Checkout: []byte(`{}`), ProjectionFingerprint: `{"queueCount":0}`}
	event := CatalogEvent{DedupeKey: "audit", Type: "session.upserted", EntityID: "audit", Payload: []byte(`{"session":{"queueCount":0}}`)}
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state.ObservedAt = time.Now()
		if _, changed, err := r.RecordCatalogSession(state, event); err != nil || changed {
			b.Fatalf("changed=%v err=%v", changed, err)
		}
	}
}

func BenchmarkAuditQueueHistory(b *testing.B) {
	for _, history := range []int{0, 10000, 50000} {
		for _, indexed := range []bool{false, true} {
			b.Run(fmt.Sprintf("history_%d/index_%v", history, indexed), func(b *testing.B) {
				r := auditRegistry(b)
				if history > 0 {
					_, err := r.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO message_queue(session_id,message,status,delivered) SELECT 'audit','historical message','delivered',1 FROM n`, history)
					if err != nil {
						b.Fatal(err)
					}
				}
				if _, err := r.db.Exec(`INSERT INTO message_queue(session_id,message,status) VALUES('audit','pending message','pending')`); err != nil {
					b.Fatal(err)
				}
				if indexed {
					if _, err := r.db.Exec(`CREATE INDEX audit_queue_active ON message_queue(session_id,status) WHERE status IN ('pending','inflight')`); err != nil {
						b.Fatal(err)
					}
				}
				b.Run("QueueCount", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						if n := r.QueueCount("audit"); n != 1 {
							b.Fatalf("count=%d", n)
						}
					}
				})
				b.Run("QueueCounts", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						counts, err := r.QueueCounts()
						if err != nil || counts["audit"] != 1 {
							b.Fatalf("counts=%v err=%v", counts, err)
						}
					}
				})
			})
		}
	}
}

func BenchmarkAuditJournalRetentionRead(b *testing.B) {
	for _, count := range []int{1, 256, 2048} {
		b.Run(fmt.Sprintf("rows_%d", count), func(b *testing.B) {
			r := auditRegistry(b)
			for i := 0; i < count; i++ {
				if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "audit", Kind: "text", ProviderKey: fmt.Sprint(i), Payload: []byte(`{"body":"seed"}`)}, SessionJournalRetention{Count: 2048, Bytes: 8 << 20}); err != nil {
					b.Fatal(err)
				}
			}
			for _, withCount := range []bool{true, false} {
				b.Run(fmt.Sprintf("with_count_%v", withCount), func(b *testing.B) {
					query := journalRetainedSQL
					if !withCount {
						query = `SELECT 0,retained_bytes FROM session_journal_state WHERE session_id=? AND session_id=?`
					}
					stmt, err := r.read.Prepare(query)
					if err != nil {
						b.Fatal(err)
					}
					defer stmt.Close()
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						var rows, bytes int
						if err := stmt.QueryRow("audit", "audit").Scan(&rows, &bytes); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestAuditQueuePlans(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "audit")
	for _, indexed := range []bool{false, true} {
		if indexed {
			if _, err := r.db.Exec(`CREATE INDEX audit_queue_active ON message_queue(session_id,status) WHERE status IN ('pending','inflight')`); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []string{
			`SELECT COUNT(*) FROM message_queue WHERE session_id='audit' AND status IN ('pending','inflight')`,
			`SELECT session_id,COUNT(*) FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id`,
		} {
			rows, err := r.db.Query("EXPLAIN QUERY PLAN " + query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				t.Logf("indexed=%v plan=%s", indexed, detail)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
