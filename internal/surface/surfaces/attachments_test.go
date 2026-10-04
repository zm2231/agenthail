package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
