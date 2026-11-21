package codexsurface

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
