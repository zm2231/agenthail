package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	providers "github.com/zm2231/agenthail/internal/surface/surfaces"
)

const (
	revertedThread  = "019ff71a-62ce-77b1-9c16-8db852170d46"
	revertedRollout = "01a0a482-5500-7000-8000-000000000001"
)

func revertTurnRecords(turn, user, assistant string) string {
	return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"task_started","turn_id":%q}}
{"type":"response_item","payload":{"id":%q,"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}
{"type":"response_item","payload":{"id":%q,"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":%q}}
`, turn, turn+"-user", user, turn+"-assistant", assistant, turn)
}

type revertFixture struct {
	original string
	cut      int
}

func writeRevertOriginal(t *testing.T) revertFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"history_mode":"paginated"}}`, revertedThread) + "\n"
	first := revertTurnRecords("turn-1", "first question", "first answer")
	second := revertTurnRecords("turn-2", "reverted question", "reverted answer")
	original := filepath.Join(home, "sessions", "2026", "08", "12", "rollout-2026-08-12T13-52-09-"+revertedThread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(original), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte(meta+first+second), 0o600); err != nil {
		t.Fatal(err)
	}
	return revertFixture{original: original, cut: len(meta + first)}
}

func (f revertFixture) revert(t *testing.T) string {
	t.Helper()
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":5,"end_byte_offset":%d}}}`, revertedThread, revertedThread, f.cut) + "\n"
	continued := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(f.original)))), "2026", "09", "15", "rollout-2026-09-15T10-00-00-"+revertedThread+"_"+revertedRollout+".jsonl")
	if err := os.MkdirAll(filepath.Dir(continued), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(continued, []byte(meta+revertTurnRecords("turn-3", "new question", "new answer")), 0o600); err != nil {
		t.Fatal(err)
	}
	return continued
}

func fileIdentityForTest(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%v:%v", stat.Dev, stat.Ino)
}

func visibleJournalMessages(t *testing.T, reg *registry.Registry, sessionID string) (messages []string, sourceErrors []string) {
	t.Helper()
	page, err := reg.ReadSessionJournalPage(sessionID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	entries := page.Entries
	sort.Slice(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq })
	for _, entry := range entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		switch {
		case payload.Kind == "source-error":
			sourceErrors = append(sourceErrors, payload.Reason)
		case payload.Op == "remove":
		case payload.Role == "user" || payload.Role == "assistant":
			messages = append(messages, payload.Body)
		}
	}
	return messages, sourceErrors
}

func TestARevertedCodexThreadCatchesUpFromItsContinuationAfterARestart(t *testing.T) {
	fixture := writeRevertOriginal(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: revertedThread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: fixture.original, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if messages, _ := visibleJournalMessages(t, reg, session.ID); !slices.Equal(messages, []string{"first question", "first answer", "reverted question", "reverted answer"}) {
		t.Fatalf("seeded messages=%q", messages)
	}
	first.Cancel()
	waitForSessionSourceGone(t, manager)

	session.Transcript = fixture.revert(t)
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatalf("continued rollout was rejected: %v", err)
	}
	defer second.Cancel()
	messages, sourceErrors := visibleJournalMessages(t, reg, session.ID)
	if want := []string{"first question", "first answer", "new question", "new answer"}; !slices.Equal(messages, want) {
		t.Fatalf("messages=%q, want %q", messages, want)
	}
	if len(sourceErrors) != 0 {
		t.Fatalf("source errors=%q", sourceErrors)
	}
	info, err := os.Stat(session.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	status, _, identity, err := reg.SessionJournalSeedCheckpoint(session.ID)
	if err != nil || status != registry.SessionJournalSeeded || identity == "" || identity != fileIdentityForTest(info) {
		t.Fatalf("checkpoint status=%q identity=%q err=%v, want the continuation's", status, identity, err)
	}
}

func TestARunningCodexSourceFollowsARevertIntoTheContinuation(t *testing.T) {
	previous := sessionTranscriptMoveCheck
	sessionTranscriptMoveCheck = 20 * time.Millisecond
	t.Cleanup(func() { sessionTranscriptMoveCheck = previous })
	fixture := writeRevertOriginal(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: revertedThread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: fixture.original, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	defer manager.shutdown()
	subscription, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()

	session.Transcript = fixture.revert(t)
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	want := []string{"first question", "first answer", "new question", "new answer"}
	deadline := time.Now().Add(10 * time.Second)
	for {
		messages, sourceErrors := visibleJournalMessages(t, reg, session.ID)
		if len(sourceErrors) != 0 {
			t.Fatalf("source errors=%q", sourceErrors)
		}
		if slices.Equal(messages, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages=%q, want %q", messages, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestARevertWhoseLineageCannotBeProvenReloadsFromTheNewRolloutWithANotice(t *testing.T) {
	fixture := writeRevertOriginal(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: revertedThread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: fixture.original, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	first, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	first.Cancel()
	waitForSessionSourceGone(t, manager)

	session.Transcript = fixture.revert(t)
	if err := os.Remove(fixture.original); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatalf("unproven continuation was rejected: %v", err)
	}
	defer second.Cancel()
	messages, sourceErrors := visibleJournalMessages(t, reg, session.ID)
	if want := []string{"new question", "new answer"}; !slices.Equal(messages, want) {
		t.Fatalf("messages=%q, want %q", messages, want)
	}
	if len(sourceErrors) != 0 {
		t.Fatalf("source errors=%q", sourceErrors)
	}
	page, err := reg.ReadSessionJournalPage(session.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Kind == "notice" && payload.Title == "Conversation reloaded" && payload.Body != "" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("reload notices=%d, want 1 stating why the conversation was read again", notices)
	}
	info, err := os.Stat(session.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	status, _, identity, err := reg.SessionJournalSeedCheckpoint(session.ID)
	if err != nil || status != registry.SessionJournalSeeded || identity != fileIdentityForTest(info) {
		t.Fatalf("checkpoint status=%q identity=%q err=%v, want the new rollout's", status, identity, err)
	}
}

func TestARevertBeforeTheSeededWindowJournalsItsRetainedHistoryWithNoOlderCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"history_mode":"paginated"}}`, revertedThread) + "\n"
	first := revertTurnRecords("turn-0", "first question", "first answer")
	body := meta + first
	for turn := 1; turn <= 45; turn++ {
		body += revertTurnRecords(fmt.Sprintf("turn-%d", turn), fmt.Sprintf("question %d", turn), fmt.Sprintf("answer %d", turn))
	}
	original := filepath.Join(home, "sessions", "2026", "08", "12", "rollout-2026-08-12T13-52-09-"+revertedThread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(original), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := revertFixture{original: original, cut: len(meta + first)}
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	session := surface.Session{ID: revertedThread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Transcript: original, HasLocal: true}
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	adapter := providers.NewCodex("http://127.0.0.1:1")
	manager := newSessionSourceManager(reg)
	seeded, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	seeded.Cancel()
	waitForSessionSourceGone(t, manager)
	if page, err := reg.ReadSessionJournalPage(session.ID, 0, 200); err != nil || page.HistoryBefore <= int64(fixture.cut) {
		t.Fatalf("seeded history boundary=%d err=%v, want one past the revert point %d", page.HistoryBefore, err, fixture.cut)
	}

	session.Transcript = fixture.revert(t)
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	continued, err := manager.prepareStream(context.Background(), &session, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer continued.Cancel()
	if messages, _ := visibleJournalMessages(t, reg, session.ID); !slices.Equal(messages, []string{"first question", "first answer", "new question", "new answer"}) {
		t.Fatalf("messages=%q", messages)
	}
	page, err := reg.ReadSessionJournalPage(session.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if page.HistoryBefore != 0 || page.NextBefore != 0 {
		t.Fatalf("history before=%d next before=%d, want no older history past the journal", page.HistoryBefore, page.NextBefore)
	}
}
