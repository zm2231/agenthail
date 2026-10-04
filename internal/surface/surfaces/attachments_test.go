package surfaces

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const testPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestAttachmentResolvesOldReferencedRecordByStableOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(testPNG)
	filePath := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"path","path":` + quote(filePath) + `}}]}}` + "\n"
	content := line + `{"type":"user","uuid":"u2","message":{"content":"later"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 20)
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	var item surface.TimelineItem
	for _, candidate := range page.Items {
		if candidate.Kind == "attachment" {
			item = candidate
			break
		}
	}
	if item.Attachment == nil || item.Title != "Image" || item.Text != "Image attachment" {
		t.Fatalf("item=%+v", item)
	}
	got, bytes, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID)
	if err != nil || got.ID != item.Attachment.ID || string(bytes) != string(data) {
		t.Fatalf("attachment=%+v bytes=%d err=%v", got, len(bytes), err)
	}
	if err := os.WriteFile(filePath, append(data, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID); err == nil {
		t.Fatal("changed referenced bytes were served under immutable id")
	}
}

func quote(value string) string { b, _ := json.Marshal(value); return string(b) }

func TestAttachmentOversizedRecordPreservesTypedTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + strings.Repeat("A", 15<<20) + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, "attachment:0:0:"+strings.Repeat("0", 64))
	if !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("err=%v, want ErrAttachmentTooLarge", err)
	}
}

func TestAttachmentRejectsMidRecordOffsetAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(testPNG)
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, "attachment:1:0:"+hashBytes(data))
	if !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("mid-record err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = NewClaude("", t.TempDir()).ReadAttachment(ctx, &surface.Session{Transcript: path}, "attachment:0:0:"+hashBytes(data))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}
}

func TestAttachmentRecordAboveLegacyTimelineWindowIsProjected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(testPNG)
	data = append(data, bytes.Repeat([]byte{'x'}, 4<<20)...)
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(data) + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Attachment == nil || page.Items[0].Attachment.Bytes != int64(len(data)) {
		t.Fatalf("items=%+v err=%v", page.Items, err)
	}
}

func TestToolResultImageIsMetadataOnlyAndKeepsCallID(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString(testPNG)
	path := filepath.Join(t.TempDir(), "tool-result.jsonl")
	imagePath := filepath.Join(t.TempDir(), "tool.png")
	if err := os.WriteFile(imagePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"caption"},{"type":"image","source":{"type":"path","path":` + quote(imagePath) + `}}]}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 20)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("items=%+v err=%v", page.Items, err)
	}
	encoded, _ := json.Marshal(page.Items)
	if strings.Contains(string(encoded), testPNG) || !strings.Contains(string(encoded), "caption") {
		t.Fatalf("timeline leaked image data: %s", encoded)
	}
	if page.Items[0].Kind != "toolResult" || page.Items[0].CallID != "call-1" || page.Items[0].Text != "caption" {
		t.Fatalf("tool result=%+v", page.Items[0])
	}
	if page.Items[1].Kind != "attachment" || page.Items[1].CallID != "call-1" || page.Items[1].Attachment == nil {
		t.Fatalf("attachment=%+v", page.Items[1])
	}
}
func TestCodexLiveAttachmentSurvivesReaderRestartFromTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"look","images":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	content := line + strings.Repeat(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"later"}]}}`+"\n", 220)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: "thread-image", Transcript: path}
	digest := hashBytes(mustAttachmentData(t))
	liveID := liveAttachmentID(session.ID, digest)
	attachment, data, err := NewCodex("").ReadAttachment(context.Background(), session, liveID)
	if err != nil || attachment == nil || attachment.ID != liveID || string(data) != string(mustAttachmentData(t)) {
		t.Fatalf("attachment=%+v bytes=%d err=%v", attachment, len(data), err)
	}
	if _, _, err := NewCodex("").ReadAttachment(context.Background(), &surface.Session{ID: "other", Transcript: path}, liveID); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-session err=%v", err)
	}
}

func TestCodexLiveAttachmentCacheIsBounded(t *testing.T) {
	data := append(mustAttachmentData(t), make([]byte, 8<<20)...)
	codex := NewCodex("")
	for index := 0; index < 3; index++ {
		path := filepath.Join(t.TempDir(), "image.png")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := codex.rememberLiveAttachment(context.Background(), "thread-image", "item", index, attachmentReference{Path: path}); err != nil {
			t.Fatal(err)
		}
	}
	codex.attachmentMu.Lock()
	bytes := codex.attachmentBytes
	codex.attachmentMu.Unlock()
	if bytes > maxLiveAttachmentCacheBytes {
		t.Fatalf("cache bytes=%d limit=%d", bytes, maxLiveAttachmentCacheBytes)
	}
}

func mustAttachmentData(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(testPNG)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
