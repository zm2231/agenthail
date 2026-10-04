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
