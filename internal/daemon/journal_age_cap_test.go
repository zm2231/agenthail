package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

const claudeOldTranscript = `{"type":"user","uuid":"u1","origin":{"kind":"human"},"message":{"content":"old prompt"}}
{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"old answer"}]}}
`

func endedClaudeSession(t *testing.T, r *registry.Registry, id, cwd string) surface.Session {
	t.Helper()
	session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: cwd, HasLocal: true, Status: surface.StatusOffline, Ended: true,
		Transcript: filepath.Join(t.TempDir(), id+".jsonl")}
	if err := os.WriteFile(session.Transcript, []byte(claudeOldTranscript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	return session
}

func openUntilJournaled(t *testing.T, r *registry.Registry, session surface.Session, adapter surface.Surface) map[string]int {
	t.Helper()
	manager := newSessionSourceManager(r)
	defer manager.shutdown()
	stream, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		bodies, _ := journalBodies(t, r, session.ID)
		if bodies["old prompt"] > 0 && bodies["old answer"] > 0 {
			return bodies
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s did not journal its transcript: %v", session.ID, bodies)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func journalKept(t *testing.T, r *registry.Registry, sessionID string) bool {
	t.Helper()
	bodies, _ := journalBodies(t, r, sessionID)
	return bodies["old answer"] > 0
}

func pastTheCap() time.Time {
	return time.Now().Add(journalAgeCap + time.Hour)
}

func dropsAndReseeds(t *testing.T, d *Daemon, r *registry.Registry, session surface.Session, adapter surface.Surface) {
	t.Helper()
	openUntilJournaled(t, r, session, adapter)
	before, err := r.SessionJournalAfter(session.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	d.capJournals(context.Background(), time.Now())
	if !journalKept(t, r, session.ID) {
		t.Fatal("the cap dropped a journal younger than the cap")
	}
	d.capJournals(context.Background(), pastTheCap())
	if bodies, _ := journalBodies(t, r, session.ID); len(bodies) != 0 {
		t.Fatalf("the cap kept an ended session's old journal: %v", bodies)
	}
	if status, err := r.SessionJournalSeedStatus(session.ID); err != nil || status != registry.SessionJournalSeedUnknown {
		t.Fatalf("a dropped journal left seed status %s err=%v", status, err)
	}
	openUntilJournaled(t, r, session, adapter)
	if resumed, err := r.SessionJournalAfter(session.ID, before.LatestSeq, 1000); err != nil || !resumed.Gap || len(resumed.Entries) != 0 {
		t.Fatalf("a cursor from before the drop resumed with gap=%v entries=%d err=%v", resumed.Gap, len(resumed.Entries), err)
	}
}

func TestAnEndedClaudeSessionsOldJournalIsDroppedAndReseedsOnOpen(t *testing.T) {
	_, r, _, _, _ := daemonFixture(t)
	claude := claudesurface.NewClaude(nil, t.TempDir())
	d := newTestDaemon(t, r, []surface.Surface{claude})
	dropsAndReseeds(t, d, r, endedClaudeSession(t, r, "claude-old", t.TempDir()), claude)
}

func TestAnEndedCodexSessionWithOnlyACompressedRolloutIsDroppedAndReseedsOnOpen(t *testing.T) {
	codextest.Start(t, func(string, map[string]any) map[string]any { return nil })
	_, r, _, _, _ := daemonFixture(t)
	codex := codexsurface.NewCodex("1")
	d := newTestDaemon(t, r, []surface.Surface{codex})
	id := uuid.Must(uuid.NewV7()).String()
	writeCodexRollout(t, id, codexRolloutOnDisk{compressed: true})
	session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, HasLocal: true, Status: surface.StatusOffline, Ended: true}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	dropsAndReseeds(t, d, r, session, codex)
}

func TestTheJournalCapKeepsJournalsItCannotRebuildOrThatSomethingStillReads(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	claude := claudesurface.NewClaude(nil, t.TempDir())
	d.Surfaces = []surface.Surface{claude}
	p := addLedgerProject(t, r, "proj-cap", "cap-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	dropped := endedClaudeSession(t, r, "dropped", p.dir)
	active := endedClaudeSession(t, r, "active", p.dir)
	active.Ended, active.Status = false, surface.StatusIdle
	if err := r.RecordObservedSession(active); err != nil {
		t.Fatal(err)
	}
	gone := endedClaudeSession(t, r, "gone", p.dir)
	asked := endedClaudeSession(t, r, "asked", p.dir)
	cited := endedClaudeSession(t, r, "cited", p.dir)
	viewed := endedClaudeSession(t, r, "viewed", p.dir)
	for _, session := range []surface.Session{dropped, active, gone, asked, cited, viewed} {
		openUntilJournaled(t, r, session, claude)
	}
	if err := os.Remove(gone.Transcript); err != nil {
		t.Fatal(err)
	}
	requests, _ := json.Marshal(map[string]any{"state": map[string]any{"protocol": 1}, "requests": []map[string]string{{"messageId": "m1", "targetId": asked.ID, "turnId": "t1"}}})
	if err := os.MkdirAll(filepath.Dir(d.voiceStatePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.voiceStatePath(), requests, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: cited.ID, PromptID: "cited-1", Prompt: "Keep the old filters."}).Ref
	if code, body := ledgerWrite(t, handler, "/api/v1/ledger/attach", ledger.AttachRequest{ProjectKey: p.key, SessionID: cited.ID, UtteranceID: ref, As: ledger.AsNewAsk, Clauses: []string{"keep the old filters"}}, nil); code != 200 {
		t.Fatalf("attach %d %s", code, body)
	}
	release, err := d.sources.hold(&viewed, claude, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	d.capJournals(context.Background(), pastTheCap())
	if journalKept(t, r, dropped.ID) {
		t.Fatal("the cap kept the journal of an ended session it can rebuild")
	}
	for _, session := range []surface.Session{active, gone, asked, cited, viewed} {
		if !journalKept(t, r, session.ID) {
			t.Errorf("the cap dropped the journal of %s", session.ID)
		}
	}
}

func TestAJournalASessionSourceOpensAfterTheCapListedItIsKept(t *testing.T) {
	_, r, _, _, _ := daemonFixture(t)
	claude := claudesurface.NewClaude(nil, t.TempDir())
	session := endedClaudeSession(t, r, "reopened", t.TempDir())
	openUntilJournaled(t, r, session, claude)
	before := pastTheCap().Add(-journalAgeCap)
	stale, err := r.StaleJournalSessions(before)
	if err != nil || len(stale) != 1 {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
	if _, err := r.BeginSessionJournalSource(session.ID); err != nil {
		t.Fatal(err)
	}
	if dropped, err := r.DropStaleJournal(stale[0], before); err != nil || dropped {
		t.Fatalf("dropped=%v err=%v", dropped, err)
	}
	if !journalKept(t, r, session.ID) {
		t.Fatal("the cap dropped a journal a session source opened after it was listed")
	}
}

func TestTheJournalCapKeepsJournalsOfAnAdapterThatCannotCheckItsTranscript(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	from.Ended, from.Status = true, surface.StatusOffline
	if err := r.RecordObservedSession(from); err != nil {
		t.Fatal(err)
	}
	journalSweepRow(t, r, from.ID, "old-output", time.Now())
	d.capJournals(context.Background(), pastTheCap())
	if page, err := r.ReadSessionJournalPage(from.ID, 0, 10); err != nil || len(page.Entries) != 1 {
		t.Fatalf("the cap dropped a journal its adapter cannot rebuild: %+v err=%v", page, err)
	}
}
