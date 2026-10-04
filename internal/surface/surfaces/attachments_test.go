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
	page, err := readTimeline(context.Background(), path, "claude", 0, 20)
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
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	id := firstAttachmentID(t, path, "claude")
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 || parts[1] != "0" {
		t.Fatalf("attachment id does not reference the record offset: %q", id)
	}
	forged := parts[0] + ":1:" + parts[2]
	_, _, err := NewClaude("", t.TempDir()).ReadAttachment(context.Background(), &surface.Session{Transcript: path}, forged)
	if !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("mid-record err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = NewClaude("", t.TempDir()).ReadAttachment(ctx, &surface.Session{Transcript: path}, id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled err=%v", err)
	}
}

func firstAttachmentID(t *testing.T, path, source string) string {
	t.Helper()
	page, err := readTimeline(context.Background(), path, source, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.Kind == "attachment" && item.Attachment != nil {
			return item.Attachment.ID
		}
	}
	t.Fatalf("no attachment in %+v", page.Items)
	return ""
}

func TestAttachmentRecordAboveLegacyTimelineWindowIsProjected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data, _ := base64.StdEncoding.DecodeString(testPNG)
	data = append(data, bytes.Repeat([]byte{'x'}, 4<<20)...)
	line := `{"type":"user","uuid":"u1","message":{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + base64.StdEncoding.EncodeToString(data) + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTimeline(context.Background(), path, "claude", 0, 20)
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
	page, err := readTimeline(context.Background(), path, "claude", 0, 20)
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
func TestCodexAttachmentStaysFetchableAfterTranscriptGrows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	line := `{"type":"event_msg","payload":{"type":"user_message","message":"look","images":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + testPNG + `"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	id := firstAttachmentID(t, path, "codex")
	appendTestTranscript(t, path, strings.Repeat(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"later"}]}}`+"\n", 40000))
	attachment, data, err := NewCodex("").ReadAttachment(context.Background(), &surface.Session{ID: "thread-indexed", Transcript: path}, id)
	if err != nil || attachment == nil || attachment.ID != id || !bytes.Equal(data, mustAttachmentData(t)) {
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
	page, err := readTimeline(context.Background(), path, "codex", 0, 20)
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
	page, err := readTimeline(context.Background(), path, "codex", 0, 20)
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
	page, err := readTimeline(context.Background(), path, "claude", 0, 20)
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

func TestCodexTranscriptNormalizesInterruptionWrapperAndTypedToolOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	image := "data:image/png;base64," + testPNG
	lines := []string{
		`{"timestamp":"2026-10-04T08:09:57.401Z","type":"response_item","payload":{"type":"message","id":"msg_abort","role":"user","content":[{"type":"input_text","text":"<turn_aborted>\nThe user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed.\n</turn_aborted>"}]}}`,
		`{"timestamp":"2026-10-04T08:09:57.404Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted"}}`,
		`{"timestamp":"2026-10-04T08:10:00.000Z","type":"response_item","payload":{"type":"message","id":"msg_prose","role":"user","content":[{"type":"input_text","text":"Why did <turn_aborted> show up in my transcript?"}]}}`,
		`{"timestamp":"2026-10-04T08:10:01.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","id":"ctco_json","call_id":"call_json","output":[{"type":"input_text","text":"Script completed\nWall time 0.0 seconds\nOutput:\n"},{"type":"input_text","text":"{\"goal\":{\"status\":\"active\"}}"}]}}`,
		`{"timestamp":"2026-10-04T08:10:02.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","id":"ctco_image","call_id":"call_image","output":[{"type":"input_text","text":"screenshot"},{"type":"input_image","image_url":"` + image + `"}]}}`,
		`{"timestamp":"2026-10-04T08:10:03.000Z","type":"response_item","payload":{"type":"function_call_output","id":"fco_mixed","call_id":"call_mixed","output":[{"type":"input_text","text":"kept"},{"type":"input_audio","data":"AAAA"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := readTimeline(context.Background(), path, "codex", 0, 40)
	if err != nil {
		t.Fatal(err)
	}
	byCall := map[string][]surface.TimelineItem{}
	var notices, users, done []surface.TimelineItem
	for _, item := range page.Items {
		switch {
		case item.Kind == "event" && item.Title == "Turn interrupted":
			notices = append(notices, item)
		case item.Kind == "message" && item.Role == "user":
			users = append(users, item)
		case item.Kind == "done":
			done = append(done, item)
		case item.CallID != "":
			byCall[item.CallID] = append(byCall[item.CallID], item)
		}
		if strings.Contains(item.Text, testPNG) {
			t.Fatalf("inline image bytes leaked: %+v", item)
		}
	}
	if len(notices) != 1 || strings.Contains(notices[0].Text, "<turn_aborted>") || !strings.HasPrefix(notices[0].Text, "The user interrupted") {
		t.Fatalf("interruption notice=%+v", notices)
	}
	if len(users) != 1 || users[0].Text != "Why did <turn_aborted> show up in my transcript?" {
		t.Fatalf("ordinary user prose=%+v", users)
	}
	if len(done) != 1 || done[0].Status != "turn_aborted" || done[0].Title != "Turn interrupted" {
		t.Fatalf("lifecycle=%+v", done)
	}
	if got := byCall["call_json"]; len(got) != 1 || got[0].Kind != "toolResult" || got[0].Text != "Script completed\nWall time 0.0 seconds\nOutput:\n{\"goal\":{\"status\":\"active\"}}" {
		t.Fatalf("typed tool output=%+v", got)
	}
	imageItems := byCall["call_image"]
	if len(imageItems) != 2 || imageItems[0].Text != "screenshot" || imageItems[1].Kind != "attachment" || imageItems[1].Attachment == nil {
		t.Fatalf("tool output image=%+v", imageItems)
	}
	if _, data, err := NewCodex("").ReadAttachment(context.Background(), &surface.Session{ID: "tool-image", Transcript: path}, imageItems[1].Attachment.ID); err != nil || !bytes.Equal(data, mustAttachmentData(t)) {
		t.Fatalf("tool output image fetch bytes=%d err=%v", len(data), err)
	}
	mixed := byCall["call_mixed"]
	if len(mixed) != 1 || !strings.Contains(mixed[0].Text, `"input_audio"`) || !strings.Contains(mixed[0].Text, `"kept"`) {
		t.Fatalf("unknown mixed output was not kept losslessly: %+v", mixed)
	}
}
