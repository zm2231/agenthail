package surfaces

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func TestAttachmentResolvesOldReferencedRecordByStableOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(surfacetest.TestPNG)
	filePath := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"path","path":` + quote(filePath) + `}}]}}` + "\n"
	content := line + `{"type":"user","uuid":"u2","message":{"content":"later"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
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
	if item.Attachment == nil {
		t.Fatalf("item=%+v", item)
	}
	got, bytes, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID)
	if err != nil || got.ID != item.Attachment.ID || string(bytes) != string(data) {
		t.Fatalf("attachment=%+v bytes=%d err=%v", got, len(bytes), err)
	}
	if err := os.WriteFile(filePath, append(data, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID); err == nil {
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
	_, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, "attachment:0:0:"+strings.Repeat("0", 64))
	if !errors.Is(err, transcript.ErrAttachmentTooLarge) {
		t.Fatalf("err=%v, want ErrAttachmentTooLarge", err)
	}
}

func TestAttachmentRejectsMidRecordOffsetAndHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	id := surfacetest.FirstAttachmentID(t, timelineClaude(), path)
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || parts[1] != "0" {
		t.Fatalf("attachment id does not reference the record offset: %q", id)
	}
	forged := parts[0] + ":1:" + parts[2]
	_, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, forged)
	if !errors.Is(err, transcript.ErrAttachmentNotFound) {
		t.Fatalf("mid-record err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = NewClaude(nil, t.TempDir()).ReadAttachment(ctx, &surface.Session{Transcript: path}, id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}
}

func TestAttachmentRecordAboveLegacyTimelineWindowIsProjected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(surfacetest.TestPNG)
	data = append(data, bytes.Repeat([]byte{'x'}, 4<<20)...)
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(data) + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Attachment == nil || page.Items[0].Attachment.Bytes != int64(len(data)) {
		t.Fatalf("items=%+v err=%v", page.Items, err)
	}
}

func TestToolResultImageIsMetadataOnlyAndKeepsCallID(t *testing.T) {
	data, _ := base64.StdEncoding.DecodeString(surfacetest.TestPNG)
	path := filepath.Join(t.TempDir(), "tool-result.jsonl")
	imagePath := filepath.Join(t.TempDir(), "tool.png")
	if err := os.WriteFile(imagePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"caption"},{"type":"image","source":{"type":"path","path":` + quote(imagePath) + `}}]}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("items=%+v err=%v", page.Items, err)
	}
	encoded, _ := json.Marshal(page.Items)
	if strings.Contains(string(encoded), surfacetest.TestPNG) || !strings.Contains(string(encoded), "caption") {
		t.Fatalf("timeline leaked image data: %s", encoded)
	}
	if page.Items[0].Kind != "toolResult" || page.Items[0].CallID != "call-1" || page.Items[0].Text != "caption" {
		t.Fatalf("tool result=%+v", page.Items[0])
	}
	if page.Items[1].Kind != "attachment" || page.Items[1].CallID != "call-1" || page.Items[1].Attachment == nil {
		t.Fatalf("attachment=%+v", page.Items[1])
	}
}

func TestClaudeUnsupportedImageSourceKeepsLaterAttachmentAligned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"url","url":"https://example.invalid/a.png"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := surfacetest.ReadTimeline(context.Background(), timelineClaude(), path, 0, 20)
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
	_, data, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, attachments[1].Attachment.ID)
	if err != nil || !bytes.Equal(data, surfacetest.AttachmentData(t)) {
		t.Fatalf("aligned attachment bytes=%d err=%v", len(data), err)
	}
}

func TestAnAttachmentReadThatRanOutOfTimeIsNotReportedMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"user","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + surfacetest.TestPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(surfacetest.TestPNG)
	id := transcript.AttachmentID(0, 0, data)
	if _, _, err := transcript.ReadAttachment(context.Background(), &surface.Session{Transcript: path}, id, claudeTranscript{}, nil); err != nil {
		t.Fatalf("read in time: %v", err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, _, err := transcript.ReadAttachment(ctx, &surface.Session{Transcript: path}, id, claudeTranscript{}, nil)
	if status, code, _ := transcript.AttachmentHTTPStatus(err); status != http.StatusGatewayTimeout || code != "attachment_timeout" {
		t.Fatalf("err=%v status=%d code=%s, want 504 attachment_timeout", err, status, code)
	}
	if status, code, _ := transcript.AttachmentHTTPStatus(transcript.ErrAttachmentNotFound); status != http.StatusNotFound || code != "attachment_not_found" {
		t.Fatalf("not found: status=%d code=%s", status, code)
	}
}
