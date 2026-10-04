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
	"time"

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
func TestCodexLiveAttachmentUsesExactTranscriptReferenceAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"look","images":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "codex", int64(len(line)), 20)
	if err != nil || len(page.Items) == 0 || page.Items[1].Attachment == nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	offset, index, _, parseErr := parseAttachmentID(page.Items[1].Attachment.ID)
	if parseErr != nil {
		t.Fatal("seed attachment did not contain a durable transcript reference")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strings.Repeat(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"later"}]}}`+"\n", 40000)); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	session := &surface.Session{ID: "thread-indexed", Transcript: path}
	liveID := attachmentID(offset, index, mustAttachmentData(t))
	attachment, data, err := NewCodex("").ReadAttachment(context.Background(), session, liveID)
	if err != nil || attachment == nil || string(data) != string(mustAttachmentData(t)) {
		t.Fatalf("attachment=%+v bytes=%d err=%v", attachment, len(data), err)
	}
}

func TestCodexLiveAttachmentKeepsOffsetAfterMalformedPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"look","images":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	content := "not-json\n" + line
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var seedID string
	for _, item := range page.Items {
		if item.Kind == "attachment" {
			seedID = item.Attachment.ID
			break
		}
	}
	if seedID == "" {
		t.Fatalf("seed=%+v", page.Items)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var liveID string
	err = (&Codex{}).Stream(ctx, &surface.Session{ID: "malformed-prefix", Transcript: path, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "attachment" {
			liveID = event.Attachment.ID
			cancel()
		}
	}, time.Second)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if liveID != seedID {
		t.Fatalf("seed attachment=%q live attachment=%q", seedID, liveID)
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

func TestCodexInputImageURLSiblingsKeepDistinctFetchableIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	image := "data:image/png;base64," + testPNG
	line := `{"timestamp":"2026-09-01T12:00:00.000Z","type":"response_item","payload":{"type":"message","id":"msg_siblings","role":"user","content":[{"type":"input_text","text":"compare these"},{"type":"input_image","image_url":"` + image + `"},{"type":"input_image","image_url":"` + image + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "codex", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var seed []surface.TimelineItem
	for _, item := range page.Items {
		if item.Kind == "attachment" {
			seed = append(seed, item)
		}
	}
	if len(seed) != 2 || seed[0].Attachment == nil || seed[1].Attachment == nil {
		t.Fatalf("seed attachments=%+v", seed)
	}
	if seed[0].ID == seed[1].ID || seed[0].Attachment.ID == seed[1].Attachment.ID {
		t.Fatalf("sibling attachments share identity: %+v", seed)
	}
	for _, item := range seed {
		if item.Attachment.MediaType != "image/png" || item.Attachment.Width != 1 || item.Attachment.Height != 1 {
			t.Fatalf("attachment metadata=%+v", item.Attachment)
		}
		attachment, data, err := NewCodex("").ReadAttachment(context.Background(), &surface.Session{ID: "siblings", Transcript: path}, item.Attachment.ID)
		if err != nil || attachment.ID != item.Attachment.ID || !bytes.Equal(data, mustAttachmentData(t)) {
			t.Fatalf("fetch attachment=%+v bytes=%d err=%v", attachment, len(data), err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var live []surface.StreamEvent
	err = (&Codex{}).Stream(ctx, &surface.Session{ID: "siblings", Transcript: path, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "attachment" {
			live = append(live, event)
			if len(live) == 2 {
				cancel()
			}
		}
	}, time.Second)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(live) != 2 {
		t.Fatalf("live attachments=%+v", live)
	}
	for index, event := range live {
		if event.ProviderKey != seed[index].ID || event.Attachment == nil || event.Attachment.ID != seed[index].Attachment.ID {
			t.Fatalf("live attachment %d=%+v seed=%+v", index, event, seed[index])
		}
	}
}

func TestClaudeUnsupportedImageSourceKeepsLaterAttachmentAligned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTranscriptPage(context.Background(), path, "claude", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var attachments []surface.TimelineItem
	for _, item := range page.Items {
		if item.Kind == "attachment" {
			attachments = append(attachments, item)
		}
	}
	if len(attachments) != 2 || attachments[0].Attachment != nil || attachments[1].Attachment == nil {
		t.Fatalf("attachments=%+v", attachments)
	}
	_, data, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, attachments[1].Attachment.ID)
	if err != nil || !bytes.Equal(data, mustAttachmentData(t)) {
		t.Fatalf("aligned attachment bytes=%d err=%v", len(data), err)
	}
}
