package surfaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const testPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestClaudeAttachmentIDsAlignBetweenSeedAndFetch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := fmt.Sprintf(`{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}},{"type":"text","text":"caption"}]}}
{"type":"user","uuid":"tool","message":{"content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}]}}
`, testPNGBase64, testPNGBase64)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "s", Surface: surface.KindClaude, Transcript: path}
	page, err := claude.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var attachments []surface.TimelineItem
	for _, item := range page.Items {
		if item.Kind == "attachment" {
			attachments = append(attachments, item)
		}
	}
	if len(attachments) != 2 || attachments[0].Title != "Image" || attachments[0].Text != "Image attachment" || attachments[0].AttachmentID == attachments[1].AttachmentID || attachments[1].CallID != "call-1" {
		t.Logf("all items=%+v", page.Items)
		t.Fatalf("attachments=%+v", attachments)
	}
	for _, item := range attachments {
		attachment, data, err := claude.ReadAttachment(context.Background(), session, item.AttachmentID)
		if err != nil || attachment.MediaType != "image/png" || attachment.Width != 1 || attachment.Height != 1 || len(data) == 0 {
			t.Fatalf("item=%+v attachment=%+v bytes=%d err=%v", item, attachment, len(data), err)
		}
	}
}

func TestReadAttachmentRejectsMalformedAndOutOfScopeReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(`{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"bad"}},{"type":"image","source":{"path":%q}}]}}`+"\n", outside)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "s", Surface: surface.KindClaude, Transcript: path}
	page, err := claude.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range page.Items {
		if item.Kind == "attachment" {
			ids = append(ids, item.AttachmentID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("items=%+v", page.Items)
	}
	if _, _, err := claude.ReadAttachment(context.Background(), session, ids[0]); !errors.Is(err, ErrAttachmentInvalid) {
		t.Fatalf("malformed err=%v", err)
	}
	if _, _, err := claude.ReadAttachment(context.Background(), session, ids[1]); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("out-of-scope err=%v", err)
	}
}

func TestAttachmentSizeAndImageValidation(t *testing.T) {
	tooLarge := strings.Repeat("A", int(maxAttachmentBytes*4/3)+32)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := fmt.Sprintf(`{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":%q}}]}}`+"\n", tooLarge)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaude("Default", t.TempDir())
	session := &surface.Session{ID: "s", Surface: surface.KindClaude, Transcript: path}
	page, err := claude.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Kind != "attachment" {
		t.Fatalf("items=%+v", page.Items)
	}
	if _, _, err := claude.ReadAttachment(context.Background(), session, page.Items[0].AttachmentID); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("oversize err=%v", err)
	}
	if _, _, err := claude.ReadAttachment(context.Background(), session, "attachment:unknown"); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("missing err=%v", err)
	}
}
