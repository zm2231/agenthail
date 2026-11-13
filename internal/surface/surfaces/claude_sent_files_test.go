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

type sentEntry map[string]any

func sentFileRecord(t *testing.T, caption string, entries ...sentEntry) string {
	t.Helper()
	paths := make([]string, len(entries))
	for i, entry := range entries {
		paths[i], _ = entry["path"].(string)
	}
	result := map[string]any{"attachments": entries}
	if caption != "" {
		result["caption"] = caption
	}
	record := map[string]any{
		"type": "user", "uuid": "u1",
		"message":       map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-send", "content": "delivered"}}},
		"toolUseResult": result,
	}
	line, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	call, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a1", "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "tool_use", "id": "call-send", "name": "SendUserFile", "input": map[string]any{"files": paths, "caption": caption, "status": "normal"}},
	}}})
	return string(call) + "\n" + string(line) + "\n"
}

func writeSentFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sentItems(t *testing.T, transcript string) []surface.TimelineItem {
	t.Helper()
	path := writeSentFile(t, "session.jsonl", []byte(transcript))
	page, err := readTimeline(context.Background(), path, "claude", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []surface.TimelineItem
	for _, item := range page.Items {
		if item.Kind == "file" || item.Kind == "attachment" {
			out = append(out, item)
		}
	}
	return out
}

func readSent(t *testing.T, transcript string, item surface.TimelineItem) (*surface.Attachment, []byte, error) {
	t.Helper()
	path := writeSentFile(t, "session.jsonl", []byte(transcript))
	return NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, item.Attachment.ID)
}

func TestSentImagesBecomeFetchableAttachmentsWithTheCaptionOnTheFirst(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(testPNG)
	first, second := writeSentFile(t, "chart.png", png), writeSentFile(t, "detail.png", png)
	transcript := sentFileRecord(t, "before and after",
		sentEntry{"path": first, "size": len(png), "isImage": true, "media_type": "image/png", "pathValidated": true, "file_uuid": "f1"},
		sentEntry{"path": second, "size": len(png), "isImage": true, "media_type": nil})
	items := sentItems(t, transcript)
	if len(items) != 2 {
		t.Fatalf("items=%+v", items)
	}
	for i, item := range items {
		if item.Kind != "attachment" || item.CallID != "call-send" || item.Attachment == nil || item.Attachment.MediaType != "image/png" || item.Attachment.Name != "" || !strings.HasSuffix(item.ID, "-file") {
			t.Fatalf("item %d=%+v attachment=%+v", i, item, item.Attachment)
		}
	}
	if items[0].Text != "before and after" || items[1].Text != "Image attachment" {
		t.Fatalf("captions %q %q", items[0].Text, items[1].Text)
	}
	got, data, err := readSent(t, transcript, items[1])
	if err != nil || got.MediaType != "image/png" || !bytes.Equal(data, png) {
		t.Fatalf("attachment=%+v err=%v", got, err)
	}
	if err := os.WriteFile(second, append(png, 0), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSent(t, transcript, items[1]); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("a changed file was served under its old id: %v", err)
	}
}

func TestSentFilesTakeTheTypeTheirBytesShow(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	markdown := []byte(strings.Repeat("# Report\n\nThe failing case is row 42.\n", 120))
	binary := bytes.Repeat([]byte{0, 1, 2, 3, 0xfe, 0xff}, 1000)
	notAnImage := []byte("plain words, not pixels\n")
	cases := []struct {
		name, file, recorded, want string
		data                       []byte
		image                      bool
	}{
		{"pdf", "report.pdf", "application/pdf", "application/pdf", pdf, false},
		{"markdown keeps its recorded type over a generic sniff", "notes.md", "text/markdown", "text/markdown", markdown, false},
		{"binary with no recorded type", "dump.bin", "", "application/octet-stream", binary, false},
		{"a recorded type never overrides a specific sniff", "fake.png", "image/png", "application/pdf", pdf, false},
		{"an image hint on bytes that are not an image", "photo.png", "image/png", "image/png", notAnImage, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := writeSentFile(t, c.file, c.data)
			entry := sentEntry{"path": path, "size": len(c.data), "isImage": c.image}
			if c.recorded != "" {
				entry["media_type"] = c.recorded
			}
			transcript := sentFileRecord(t, "", entry)
			items := sentItems(t, transcript)
			if len(items) != 1 || items[0].Kind != "file" || items[0].Attachment == nil {
				t.Fatalf("items=%+v", items)
			}
			item := items[0]
			if item.Title != c.file || item.Text != "" || item.Attachment.Name != c.file || item.Attachment.MediaType != c.want || item.Attachment.Bytes != int64(len(c.data)) {
				t.Fatalf("item=%+v attachment=%+v", item, item.Attachment)
			}
			if len(item.Files) != 1 || item.Files[0].Path != path || item.Files[0].Bytes != int64(len(c.data)) {
				t.Fatalf("files=%+v", item.Files)
			}
			got, data, err := readSent(t, transcript, item)
			if err != nil || got.MediaType != c.want || got.Name != c.file || !bytes.Equal(data, c.data) {
				t.Fatalf("attachment=%+v err=%v", got, err)
			}
		})
	}
}

func TestASentFileThatIsGoneOrTooLargeStaysACardWithoutBytes(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone.txt")
	large := writeSentFile(t, "large.bin", nil)
	if err := os.Truncate(large, MaxSentFileBytes+1); err != nil {
		t.Fatal(err)
	}
	items := sentItems(t, sentFileRecord(t, "two files",
		sentEntry{"path": gone, "size": 10, "isImage": false},
		sentEntry{"path": large, "size": MaxSentFileBytes + 1, "isImage": false}))
	if len(items) != 2 || items[0].Kind != "file" || items[0].Attachment != nil || items[0].Files[0].Bytes != 0 || items[0].Text != "two files" {
		t.Fatalf("gone=%+v", items)
	}
	if items[1].Kind != "file" || items[1].Attachment != nil || items[1].Files[0].Bytes != MaxSentFileBytes+1 {
		t.Fatalf("large=%+v", items[1])
	}
}

func TestOnlyASendUserFileResultShapeMakesFileItems(t *testing.T) {
	path := writeSentFile(t, "a.txt", []byte("text"))
	for name, entry := range map[string]sentEntry{
		"relative path":    {"path": "a.txt", "size": 4, "isImage": false},
		"no image hint":    {"path": path, "size": 4},
		"no size":          {"path": path, "isImage": false},
		"path is a number": {"path": 4, "size": 4, "isImage": false},
	} {
		if items := sentItems(t, sentFileRecord(t, "", entry)); len(items) != 0 {
			t.Fatalf("%s: items=%+v", name, items)
		}
	}
}

func sentFileRecordFrom(t *testing.T, tool string, filler string, entry sentEntry) (string, int64) {
	t.Helper()
	full := sentFileRecord(t, "", entry)
	call, result, _ := strings.Cut(full, "\n")
	if tool != "SendUserFile" {
		call = strings.Replace(call, `"name":"SendUserFile"`, `"name":`+quote(tool), 1)
	}
	if tool == "" {
		call = ""
	} else {
		call += "\n"
	}
	prefix := call + filler
	return prefix + result, int64(len(prefix))
}

func forgedSentFileID(t *testing.T, transcript string, offset int64, data []byte) string {
	t.Helper()
	line, _, _ := strings.Cut(transcript[offset:], "\n")
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatal(err)
	}
	return attachmentIDFromHash(offset, len(claudeTimelineItems(record)), hashBytes(data))
}

func TestOnlyASendUserFileCallMakesItsResultServable(t *testing.T) {
	secret := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")
	path := writeSentFile(t, "id_ed25519", secret)
	entry := sentEntry{"path": path, "size": len(secret), "isImage": false}
	for name, tool := range map[string]string{"another tool's result": "mcp__files__read", "a result with no call": ""} {
		t.Run(name, func(t *testing.T) {
			transcript, offset := sentFileRecordFrom(t, tool, "", entry)
			if items := sentItems(t, transcript); len(items) != 0 {
				t.Fatalf("items=%+v", items)
			}
			session := &surface.Session{Transcript: writeSentFile(t, "session.jsonl", []byte(transcript))}
			if _, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), session, forgedSentFileID(t, transcript, offset, secret)); !errors.Is(err, ErrAttachmentNotFound) {
				t.Fatalf("a forged id served the path: %v", err)
			}
		})
	}
}

func TestACallWrittenAfterItsResultDoesNotMakeItServable(t *testing.T) {
	data := []byte("notes\n")
	path := writeSentFile(t, "notes.txt", data)
	call, result, _ := strings.Cut(sentFileRecord(t, "", sentEntry{"path": path, "size": len(data), "isImage": false}), "\n")
	transcript := result + call + "\n"
	if items := sentItems(t, transcript); len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
	session := &surface.Session{Transcript: writeSentFile(t, "session.jsonl", []byte(transcript))}
	if _, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), session, forgedSentFileID(t, transcript, 0, data)); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("a later call made the result servable: %v", err)
	}
}

func TestASentFileWhoseCallIsFarBackIsFoundUntilTheReadBackLimit(t *testing.T) {
	data := []byte("notes\n")
	path := writeSentFile(t, "notes.txt", data)
	entry := sentEntry{"path": path, "size": len(data), "isImage": false}
	filler := func(bytes int64) string {
		text := strings.Repeat("x", 256<<10)
		record, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "f", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}})
		var b strings.Builder
		for int64(b.Len()) < bytes {
			b.Write(record)
			b.WriteByte('\n')
		}
		return b.String()
	}
	near, nearOffset := sentFileRecordFrom(t, "SendUserFile", filler(1<<20), entry)
	session := &surface.Session{Transcript: writeSentFile(t, "near.jsonl", []byte(near))}
	got, served, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), session, forgedSentFileID(t, near, nearOffset, data))
	if err != nil || got.Name != "notes.txt" || !bytes.Equal(served, data) {
		t.Fatalf("a call 1 MiB back: attachment=%+v err=%v", got, err)
	}
	far, farOffset := sentFileRecordFrom(t, "SendUserFile", filler(claudeCallReadBackMost+(256<<10)), entry)
	session = &surface.Session{Transcript: writeSentFile(t, "far.jsonl", []byte(far))}
	if _, _, err := NewClaude(nil, t.TempDir()).ReadAttachment(context.Background(), session, forgedSentFileID(t, far, farOffset, data)); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("a call past the read-back limit was trusted: %v", err)
	}
}
