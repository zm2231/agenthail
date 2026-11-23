package codexsurface

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func compressRollout(t *testing.T, plain string, appended ...string) string {
	t.Helper()
	body, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range appended {
		body = append(body, line+"\n"...)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain+".zst", encoder.EncodeAll(body, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	return plain + ".zst"
}

func TestACodexAttachmentStaysFetchableOnceItsRolloutIsCompressed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"look","images":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{}}`+"\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	id := surfacetest.FirstAttachmentID(t, &Codex{}, path)
	compressed := compressRollout(t, path)
	_, data, err := surfacetest.InDaemon(NewCodex("")).ReadAttachment(context.Background(), &surface.Session{ID: "compressed", Transcript: compressed}, id)
	if err != nil || !bytes.Equal(data, surfacetest.AttachmentData(t)) {
		t.Fatalf("attachment from a compressed rollout: bytes=%d err=%v", len(data), err)
	}
}

func TestACodexStreamContinuesWhenItsRolloutIsCompressed(t *testing.T) {
	surfacetest.SlowPoll(t)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n"+`{"type":"response_item","payload":{"id":"user-1","type":"message","role":"user","content":[{"type":"input_text","text":"before compression"}]}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := &Codex{desktopURL: "ws://127.0.0.1:1"}
	session := &surface.Session{ID: "compressing", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusBusy}
	seed, err := codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	if err != nil || !seed.TranscriptOffsetSet {
		t.Fatalf("seed=%+v err=%v", seed, err)
	}
	streamSession := *session
	streamSession.TranscriptOffset, streamSession.TranscriptOffsetSet, streamSession.TranscriptIdentity = seed.TranscriptOffset, true, seed.TranscriptIdentity
	waits := surfacetest.StreamWaits(t)
	texts := make(chan string, 64)
	ended := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ended <- codex.Stream(ctx, &streamSession, "", func(event surface.StreamEvent) {
			if event.Text != "" {
				texts <- event.Text
			}
		}, time.Minute)
	}()
	t.Cleanup(func() { cancel(); <-ended })
	surfacetest.AwaitWaitBeyond(t, waits, 0)
	compressRollout(t, path, `{"type":"response_item","payload":{"id":"answer-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"after compression"}]}}`)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case text := <-texts:
			if text == "before compression" {
				t.Fatal("the stream replayed the rollout from the start once it was compressed")
			}
			if text == "after compression" {
				return
			}
		case err := <-ended:
			ended <- err
			t.Fatalf("the stream ended once its rollout was compressed: %v", err)
		case <-deadline:
			t.Fatal("the stream did not serve activity written into the compressed rollout")
		}
	}
}

func TestACatalogSweepOverCompressedRolloutsDecompressesNothing(t *testing.T) {
	const (
		rootID   = "019f004a-0000-7000-8000-0000000000c0"
		parentID = "019f004a-0000-7000-8000-0000000000c1"
	)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	var threads []any
	var rollouts []string
	daemon := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{}}
		case "thread/list":
			return map[string]any{"data": threads}
		}
		return nil
	})
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(daemon.Home, "sessions", "2026", "10", "01")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := range 200 {
		id := fmt.Sprintf("019f004a-0000-7000-8000-%012x", 0xd00+index)
		path := filepath.Join(dir, "rollout-2026-10-01T10-00-00-"+id+".jsonl.zst")
		body := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"session_id":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":%q,"depth":2}}}}}`, id, rootID, parentID) + "\n" +
			`{"timestamp":"2026-10-01T10:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn"}}` + "\n" +
			`{"timestamp":"2026-10-01T10:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":10},"model_context_window":100}}}` + "\n"
		if err := os.WriteFile(path, encoder.EncodeAll([]byte(body), nil), 0o600); err != nil {
			t.Fatal(err)
		}
		thread := map[string]any{"id": id, "name": id, "source": "vscode", "status": map[string]any{"type": "notLoaded"}, "recencyAt": float64(1790000000), "path": path}
		if index%2 == 1 {
			thread["source"] = map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parentID, "depth": 2}}}
		}
		threads = append(threads, thread)
		rollouts = append(rollouts, path)
	}
	codex := isolatedManagedRuntime(t)
	sessions, complete, err := codex.CatalogList(context.Background())
	if err != nil || !complete {
		t.Fatalf("sweep complete=%v err=%v", complete, err)
	}
	if len(sessions) != len(rollouts) {
		t.Fatalf("swept %d sessions, want %d", len(sessions), len(rollouts))
	}
	for _, session := range sessions {
		if !session.HasLocal || !session.SettledTranscript || !strings.HasSuffix(session.Transcript, ".zst") {
			t.Fatalf("session %s transcript=%q local=%v settled=%v, want its compressed rollout, settled", session.ID, session.Transcript, session.HasLocal, session.SettledTranscript)
		}
		if session.Subagent != nil && session.Subagent.RootID != rootID {
			t.Fatalf("subagent %s root=%q, want %q from its compressed rollout's header", session.ID, session.Subagent.RootID, rootID)
		}
		if _, err := codex.LocalStatus(context.Background(), session); err != nil {
			t.Fatal(err)
		}
		codex.Background(context.Background(), session)
		if _, err := codex.ContextUsage(context.Background(), &session); err != nil {
			t.Fatal(err)
		}
	}
	if copies, _ := filepath.Glob(filepath.Join(cache, "*", "*", "*")); len(copies) > 0 {
		t.Fatalf("background reads wrote %d decompressed copies", len(copies))
	}
}
