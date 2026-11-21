package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func TestAClaudeSessionMarkedSeededWithAnEmptyJournalReseedsFromItsTranscript(t *testing.T) {
	_, reg, _, from, _ := daemonFixture(t)
	from.ID = "claude-stuck-empty"
	from.Surface = surface.KindClaude
	from.RuntimeKind = surface.RuntimeClaudeCLI
	from.HasLocal = true
	from.Transcript = filepath.Join(t.TempDir(), from.ID+".jsonl")
	if err := os.WriteFile(from.Transcript, []byte(`{"type":"user","uuid":"u1","origin":{"kind":"human"},"message":{"content":"old prompt"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"old answer"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordObservedSession(from); err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkSessionJournalSeedWithIdentity(from.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	stream, err := newTestSourceManager(t, reg).prepareStream(context.Background(), &from, claudesurface.NewClaude(nil, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		bodies, _ := journalBodies(t, reg, from.ID)
		if bodies["old prompt"] > 0 && bodies["old answer"] > 0 {
			return
		}
		if time.Now().After(deadline) {
			status, _ := reg.SessionJournalSeedStatus(from.ID)
			if status != registry.SessionJournalSeeded {
				t.Fatalf("seed status=%s", status)
			}
			t.Fatalf("a Claude session stuck seeded with an empty journal stayed empty: %v", bodies)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestASessionWhoseTranscriptIsNotOnDiskIsNotMarkedSeeded(t *testing.T) {
	codextest.Start(t, func(string, map[string]any) map[string]any { return nil })
	_, reg, _, _, _ := daemonFixture(t)
	session := surface.Session{ID: uuid.Must(uuid.NewV7()).String(), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, HasLocal: true}
	if err := reg.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	stream, err := newTestSourceManager(t, reg).prepareStream(context.Background(), &session, codexsurface.NewCodex("1"))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	if status, err := reg.SessionJournalSeedStatus(session.ID); err != nil || status == registry.SessionJournalSeeded {
		t.Fatalf("a session with no transcript on disk was marked seeded: status=%s err=%v", status, err)
	}
}
