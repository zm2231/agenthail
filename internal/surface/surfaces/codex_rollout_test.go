package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	continuedThread  = "019ff71a-62ce-77b1-9c16-8db852170d46"
	continuedRollout = "01a0a482-5500-7000-8000-000000000001"
)

func continuedTurnRecords(turn, user, assistant string, ordinal *int) string {
	records := []string{
		fmt.Sprintf(`{"type":"event_msg","payload":{"type":"task_started","turn_id":%q}}`, turn),
		fmt.Sprintf(`{"type":"response_item","payload":{"id":%q,"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}`, turn+"-user", user),
		fmt.Sprintf(`{"type":"response_item","payload":{"id":%q,"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}}`, turn+"-assistant", assistant),
		fmt.Sprintf(`{"type":"event_msg","payload":{"type":"task_complete","turn_id":%q}}`, turn),
	}
	out := ""
	for _, record := range records {
		*ordinal++
		out += record[:len(record)-1] + fmt.Sprintf(`,"ordinal":%d}`, *ordinal) + "\n"
	}
	return out
}

func writeContinuedRollout(t *testing.T) (original, continued string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	ordinal := 0
	meta := fmt.Sprintf(`{"type":"session_meta","ordinal":0,"payload":{"id":%q,"history_mode":"paginated"}}`, continuedThread) + "\n"
	first := continuedTurnRecords("turn-1", "first question", "first answer", &ordinal)
	cutOrdinal := ordinal + 1
	second := continuedTurnRecords("turn-2", "reverted question", "reverted answer", &ordinal)
	original = filepath.Join(home, "sessions", "2026", "08", "12", "rollout-2026-08-12T13-52-09-"+continuedThread+".jsonl")
	if err := os.MkdirAll(filepath.Dir(original), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte(meta+first+second), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinal = cutOrdinal
	revertMeta := fmt.Sprintf(`{"type":"session_meta","ordinal":%d,"payload":{"id":%q,"history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":%d,"end_byte_offset":%d}}}`, cutOrdinal, continuedThread, continuedThread, cutOrdinal, len(meta+first)) + "\n"
	third := continuedTurnRecords("turn-3", "new question", "new answer", &ordinal)
	continued = filepath.Join(home, "sessions", "2026", "09", "15", "rollout-2026-09-15T10-00-00-"+continuedThread+"_"+continuedRollout+".jsonl")
	if err := os.MkdirAll(filepath.Dir(continued), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(continued, []byte(revertMeta+third), 0o600); err != nil {
		t.Fatal(err)
	}
	return original, continued
}

func messageTexts(items []surface.TimelineItem) []string {
	var texts []string
	for _, item := range items {
		if item.Kind == "message" {
			texts = append(texts, item.Text)
		}
	}
	return texts
}

func TestAContinuedRolloutReadsItsRetainedHistoryWithoutTheRevertedTurn(t *testing.T) {
	_, continued := writeContinuedRollout(t)
	page, err := readCodexTranscriptPage(context.Background(), continued, continuedThread, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(page.Items), []string{"first question", "first answer", "new question", "new answer"}; !slices.Equal(got, want) {
		t.Fatalf("messages=%q, want %q", got, want)
	}
	info, err := os.Stat(continued)
	if err != nil {
		t.Fatal(err)
	}
	if page.TranscriptIdentity != transcriptFileIdentity(info) || !page.TranscriptOffsetSet || page.TranscriptOffset != info.Size() {
		t.Fatalf("identity=%q offset=%d set=%v, want the continuation's %q at %d", page.TranscriptIdentity, page.TranscriptOffset, page.TranscriptOffsetSet, transcriptFileIdentity(info), info.Size())
	}
}

func TestOlderPagesOfAContinuedRolloutCrossIntoTheFileItContinues(t *testing.T) {
	_, continued := writeContinuedRollout(t)
	newest, err := readCodexTranscriptPage(context.Background(), continued, continuedThread, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(newest.Items), []string{"new question", "new answer"}; !slices.Equal(got, want) {
		t.Fatalf("newest messages=%q, want %q", got, want)
	}
	if newest.NextBefore <= 0 {
		t.Fatalf("next before=%d, want a cursor into the earlier rollout", newest.NextBefore)
	}
	older, err := readCodexTranscriptPage(context.Background(), continued, continuedThread, newest.NextBefore, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(older.Items), []string{"first question", "first answer"}; !slices.Equal(got, want) {
		t.Fatalf("older messages=%q, want %q", got, want)
	}
	if older.NextBefore != 0 {
		t.Fatalf("next before=%d, want the start of history", older.NextBefore)
	}
	if older.TranscriptIdentity != newest.TranscriptIdentity {
		t.Fatalf("older page identity=%q, want the continuation's %q", older.TranscriptIdentity, newest.TranscriptIdentity)
	}
}

func TestAContinuationReportsTheItemsItRevertedFromTheTrustedRollout(t *testing.T) {
	original, continued := writeContinuedRollout(t)
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	codex := NewCodex("http://127.0.0.1:1")
	session := surface.Session{ID: continuedThread, Surface: surface.KindCodex, Transcript: continued}
	dropped, continues, err := codex.TranscriptContinuation(context.Background(), &session, "", transcriptFileIdentity(info))
	if err != nil || !continues {
		t.Fatalf("continues=%v err=%v, want a continuation of the trusted rollout", continues, err)
	}
	if got, want := messageTexts(dropped), []string{"reverted question", "reverted answer"}; !slices.Equal(got, want) {
		t.Fatalf("dropped=%q, want %q", got, want)
	}
}

func TestACompressedTrustedRolloutIsMatchedByItsPath(t *testing.T) {
	original, continued := writeContinuedRollout(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	plain, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original+".zst", encoder.EncodeAll(plain, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	codex := NewCodex("http://127.0.0.1:1")
	session := surface.Session{ID: continuedThread, Surface: surface.KindCodex, Transcript: continued}
	dropped, continues, err := codex.TranscriptContinuation(context.Background(), &session, original, "0:0")
	if err != nil || !continues {
		t.Fatalf("continues=%v err=%v, want the compressed rollout matched by path", continues, err)
	}
	if got, want := messageTexts(dropped), []string{"reverted question", "reverted answer"}; !slices.Equal(got, want) {
		t.Fatalf("dropped=%q, want %q", got, want)
	}
	page, err := readCodexTranscriptPage(context.Background(), continued, continuedThread, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(page.Items), []string{"first question", "first answer", "new question", "new answer"}; !slices.Equal(got, want) {
		t.Fatalf("messages=%q, want %q", got, want)
	}
}

func TestARolloutThatDoesNotContinueTheTrustedOneIsNotAContinuation(t *testing.T) {
	original, continued := writeContinuedRollout(t)
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(continued), "rollout-2026-09-15T10-00-00-"+continuedThread+"_01a0a482-5500-7000-8000-000000000002.jsonl")
	if err := os.WriteFile(unrelated, []byte(fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"history_mode":"paginated"}}`, continuedThread)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := NewCodex("http://127.0.0.1:1")
	session := surface.Session{ID: continuedThread, Surface: surface.KindCodex, Transcript: unrelated}
	if _, continues, err := codex.TranscriptContinuation(context.Background(), &session, original, transcriptFileIdentity(info)); err != nil || continues {
		t.Fatalf("continues=%v err=%v, want a rollout without history_base rejected", continues, err)
	}
	other := filepath.Join(filepath.Dir(continued), "rollout-2026-09-15T10-00-00-"+continuedThread+"_01a0a482-5500-7000-8000-000000000003.jsonl")
	if err := os.WriteFile(other, []byte(`{"type":"session_meta","payload":{"id":"another-thread","history_base":{"thread_id":"`+continuedThread+`","end_ordinal_exclusive":1,"end_byte_offset":1}}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session.Transcript = other
	if _, continues, err := codex.TranscriptContinuation(context.Background(), &session, original, transcriptFileIdentity(info)); err != nil || continues {
		t.Fatalf("continues=%v err=%v, want another thread's rollout rejected", continues, err)
	}
}

func TestTwoRevertsReadAsOneHistoryAndReportOnlyTheTrustedRolloutsRevertedTurn(t *testing.T) {
	original, continued := writeContinuedRollout(t)
	plain, err := os.ReadFile(continued)
	if err != nil {
		t.Fatal(err)
	}
	header := plain[:slices.Index(plain, '\n')+1]
	secondRollout := "01a0a4b9-4380-7000-8000-000000000004"
	ordinal := 9
	meta := fmt.Sprintf(`{"type":"session_meta","ordinal":10,"payload":{"id":%q,"history_mode":"paginated","history_base":{"thread_id":%q,"end_ordinal_exclusive":10,"end_byte_offset":%d}}}`, continuedThread, continuedRollout, len(header)) + "\n"
	latest := filepath.Join(filepath.Dir(continued), "rollout-2026-09-15T11-00-00-"+continuedThread+"_"+secondRollout+".jsonl")
	if err := os.WriteFile(latest, []byte(meta+continuedTurnRecords("turn-4", "latest question", "latest answer", &ordinal)), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := readCodexTranscriptPage(context.Background(), latest, continuedThread, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageTexts(page.Items), []string{"first question", "first answer", "latest question", "latest answer"}; !slices.Equal(got, want) {
		t.Fatalf("messages=%q, want %q", got, want)
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	codex := NewCodex("http://127.0.0.1:1")
	session := surface.Session{ID: continuedThread, Surface: surface.KindCodex, Transcript: latest}
	dropped, continues, err := codex.TranscriptContinuation(context.Background(), &session, "", transcriptFileIdentity(info))
	if err != nil || !continues {
		t.Fatalf("continues=%v err=%v", continues, err)
	}
	if got, want := messageTexts(dropped), []string{"reverted question", "reverted answer"}; !slices.Equal(got, want) {
		t.Fatalf("dropped=%q, want %q", got, want)
	}
}
