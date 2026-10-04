package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const maxAttachmentBytes int64 = 10 << 20
const maxAttachmentRecordBytes int64 = 16 << 20

var ErrAttachmentNotFound = errors.New("attachment not found")
var ErrAttachmentTooLarge = errors.New("attachment exceeds 10 MiB")
var ErrAttachmentInvalid = errors.New("attachment is not a supported image")

type attachmentReference struct{ MediaType, Data, Path string }

func (c *Claude) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	return readTranscriptAttachment(ctx, s, id, "claude")
}
func (c *Codex) ReadAttachment(ctx context.Context, s *surface.Session, id string) (*surface.Attachment, []byte, error) {
	if attachment, data, ok := c.readLiveAttachment(ctx, s, id); ok {
		return attachment, data, nil
	}
	if strings.HasPrefix(id, "live-attachment:") {
		return c.readDurableLiveAttachment(ctx, s, id)
	}
	return readTranscriptAttachment(ctx, s, id, "codex")
}

func (c *Codex) rememberLiveAttachment(ctx context.Context, sessionID, itemID string, index int, ref attachmentReference) (*surface.Attachment, error) {
	data, err := readAttachmentReference(ctx, ref)
	if err != nil {
		return nil, err
	}
	media, width, height, err := validateAttachment(data)
	if err != nil {
		return nil, err
	}
	id := liveAttachmentID(sessionID, hashBytes(data))
	attachment := surface.Attachment{ID: id, MediaType: media, Width: width, Height: height, Bytes: int64(len(data))}
	key := sessionID + "\x00" + id
	c.attachmentMu.Lock()
	if c.attachments == nil {
		c.attachments = map[string]liveAttachment{}
	}
	_, existed := c.attachments[key]
	if existing, ok := c.attachments[key]; ok {
		c.attachmentBytes -= int64(len(existing.data))
	}
	dataCopy := append([]byte(nil), data...)
	c.attachments[key] = liveAttachment{key: key, meta: attachment, data: dataCopy}
	if !existed {
		c.attachmentOrder = append(c.attachmentOrder, key)
	}
	c.attachmentBytes += int64(len(dataCopy))
	for c.attachmentBytes > maxLiveAttachmentCacheBytes && len(c.attachmentOrder) > 0 {
		oldest := c.attachmentOrder[0]
		c.attachmentOrder = c.attachmentOrder[1:]
		cached, ok := c.attachments[oldest]
		if !ok {
			continue
		}
		delete(c.attachments, oldest)
		c.attachmentBytes -= int64(len(cached.data))
	}
	c.attachmentMu.Unlock()
	return &attachment, nil
}

func liveAttachmentID(sessionID, digest string) string {
	return "live-attachment:" + sessionID + ":" + digest
}

func liveAttachmentParts(id string) (string, string, bool) {
	prefix := "live-attachment:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", false
	}
	separator := strings.LastIndex(id, ":")
	if separator <= len(prefix) || len(id)-separator-1 != 64 {
		return "", "", false
	}
	sessionID := id[len(prefix):separator]
	digest := id[separator+1:]
	if _, err := hex.DecodeString(digest); err != nil {
		return "", "", false
	}
	return sessionID, digest, true
}

func (c *Codex) readLiveAttachment(ctx context.Context, session *surface.Session, id string) (*surface.Attachment, []byte, bool) {
	if session == nil || !strings.HasPrefix(id, "live-attachment:") {
		return nil, nil, false
	}
	owner, _, ok := liveAttachmentParts(id)
	if !ok || owner != session.ID {
		return nil, nil, false
	}
	select {
	case <-ctx.Done():
		return nil, nil, false
	default:
	}
	c.attachmentMu.Lock()
	attachment, ok := c.attachments[session.ID+"\x00"+id]
	c.attachmentMu.Unlock()
	if !ok {
		return nil, nil, false
	}
	data := append([]byte(nil), attachment.data...)
	meta := attachment.meta
	return &meta, data, true
}

func (c *Codex) readDurableLiveAttachment(ctx context.Context, session *surface.Session, id string) (*surface.Attachment, []byte, error) {
	owner, digest, ok := liveAttachmentParts(id)
	if !ok || session == nil || owner != session.ID {
		return nil, nil, ErrAttachmentNotFound
	}
	path := session.Transcript
	if path == "" {
		path = codexTranscriptPath(session)
	}
	return readCodexLiveAttachmentFromTranscript(ctx, path, id, digest)
}

func readCodexLiveAttachmentFromTranscript(ctx context.Context, path, id, digest string) (*surface.Attachment, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, ErrAttachmentNotFound
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		line, err := readBoundedLine(reader)
		if len(line) > 0 {
			var record map[string]any
			if json.Unmarshal(line, &record) == nil {
				items := codexTimelineItems(record)
				refs := codexAttachmentReferences(record)
				refIndex := 0
				for _, item := range items {
					if item.Kind != "attachment" {
						continue
					}
					if refIndex >= len(refs) {
						break
					}
					ref := refs[refIndex]
					refIndex++
					data, readErr := readAttachmentReference(ctx, ref)
					if readErr != nil || hashBytes(data) != digest {
						continue
					}
					media, width, height, validateErr := validateAttachment(data)
					if validateErr != nil {
						continue
					}
					return &surface.Attachment{ID: id, MediaType: media, Width: width, Height: height, Bytes: int64(len(data))}, data, nil
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil, nil, ErrAttachmentNotFound
		}
		if err != nil {
			return nil, nil, err
		}
	}
}

func readBoundedLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if int64(len(line))+int64(len(part)) > maxAttachmentRecordBytes {
			return nil, ErrAttachmentTooLarge
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

func readTranscriptAttachment(ctx context.Context, s *surface.Session, id, source string) (*surface.Attachment, []byte, error) {
	path := s.Transcript
	if path == "" {
		if source == "claude" {
			path = NewClaude("", "").transcriptPath(s)
		} else {
			path = codexTranscriptPath(s)
		}
	}
	offset, index, expected, err := parseAttachmentID(id)
	if err != nil || path == "" {
		return nil, nil, ErrAttachmentNotFound
	}
	ref, ok, err := readAttachmentRecord(ctx, path, source, offset, index)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, ErrAttachmentNotFound
	}
	data, err := readAttachmentReference(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if hashBytes(data) != expected {
		return nil, nil, ErrAttachmentNotFound
	}
	media, width, height, err := validateAttachment(data)
	if err != nil {
		return nil, nil, err
	}
	return &surface.Attachment{ID: id, MediaType: media, Width: width, Height: height, Bytes: int64(len(data))}, data, nil
}

func parseAttachmentID(id string) (int64, int, string, error) {
	p := strings.Split(id, ":")
	if len(p) != 4 || p[0] != "attachment" || len(p[3]) != 64 {
		return 0, 0, "", ErrAttachmentNotFound
	}
	o, err := strconv.ParseInt(p[1], 10, 64)
	i, ie := strconv.Atoi(p[2])
	if err != nil || ie != nil || o < 0 || i < 0 {
		return 0, 0, "", ErrAttachmentNotFound
	}
	if _, err := hex.DecodeString(p[3]); err != nil {
		return 0, 0, "", ErrAttachmentNotFound
	}
	return o, i, p[3], nil
}

func readAttachmentRecord(ctx context.Context, path, source string, offset int64, index int) (attachmentReference, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return attachmentReference{}, false, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return attachmentReference{}, false, err
	}
	if offset > 0 {
		var previous [1]byte
		if _, err = f.ReadAt(previous[:], offset-1); err != nil || previous[0] != '\n' {
			return attachmentReference{}, false, nil
		}
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			return attachmentReference{}, false, err
		}
	}
	r := bufio.NewReaderSize(f, 64*1024)
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return attachmentReference{}, false, err
		}
		part, e := r.ReadSlice('\n')
		if int64(len(line))+int64(len(part)) > maxAttachmentRecordBytes {
			return attachmentReference{}, false, ErrAttachmentTooLarge
		}
		line = append(line, part...)
		if e == bufio.ErrBufferFull {
			continue
		}
		if e == nil {
			break
		}
		if e == io.EOF {
			break
		}
		return attachmentReference{}, false, e
	}
	if err := ctx.Err(); err != nil {
		return attachmentReference{}, false, err
	}
	var record map[string]any
	if json.Unmarshal(line, &record) != nil {
		return attachmentReference{}, false, nil
	}
	items := timelineItemsForSource(record, source)
	refs := attachmentReferencesForSource(record, source)
	refIndex := 0
	for itemIndex, item := range items {
		if item.Kind != "attachment" {
			continue
		}
		if itemIndex == index && refIndex < len(refs) {
			return refs[refIndex], true, nil
		}
		refIndex++
	}
	return attachmentReference{}, false, nil
}

func readAttachmentReference(ctx context.Context, ref attachmentReference) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if ref.Data != "" {
		p := strings.SplitN(ref.Data, ",", 2)
		if len(p) != 2 || !strings.HasSuffix(strings.ToLower(p[0]), ";base64") {
			return nil, ErrAttachmentInvalid
		}
		if int64(len(p[1])) > maxAttachmentBytes*4/3+8 {
			return nil, ErrAttachmentTooLarge
		}
		data, err := base64.StdEncoding.DecodeString(p[1])
		if err != nil {
			return nil, ErrAttachmentInvalid
		}
		if int64(len(data)) > maxAttachmentBytes {
			return nil, ErrAttachmentTooLarge
		}
		return data, nil
	}
	if !filepath.IsAbs(ref.Path) {
		return nil, ErrAttachmentNotFound
	}
	info, err := os.Lstat(ref.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAttachmentNotFound
	}
	f, err := os.Open(ref.Path)
	if err != nil {
		return nil, ErrAttachmentNotFound
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return nil, ErrAttachmentNotFound
	}
	data := make([]byte, 0, 64*1024)
	chunk := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		read, readErr := f.Read(chunk)
		if read > 0 {
			if int64(len(data)+read) > maxAttachmentBytes {
				return nil, ErrAttachmentTooLarge
			}
			data = append(data, chunk[:read]...)
		}
		if readErr == io.EOF {
			return data, nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrAttachmentNotFound
		}
	}
}

func validateAttachment(data []byte) (string, int, int, error) {
	media := http.DetectContentType(data)
	if media != "image/png" && media != "image/jpeg" && media != "image/gif" {
		return "", 0, 0, ErrAttachmentInvalid
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, ErrAttachmentInvalid
	}
	return media, cfg.Width, cfg.Height, nil
}
func hashBytes(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func attachmentID(offset int64, index int, data []byte) string {
	return "attachment:" + strconv.FormatInt(offset, 10) + ":" + strconv.Itoa(index) + ":" + hashBytes(data)
}

func attachmentReferenceFromValue(v any) (attachmentReference, bool) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "data:") {
			return attachmentReference{Data: x}, true
		}
		if filepath.IsAbs(x) {
			return attachmentReference{Path: x}, true
		}
	case map[string]any:
		if str(x, "type") == "image" {
			return attachmentReferenceFromValue(x["source"])
		}
		media := str(x, "media_type")
		if media == "" {
			media = str(x, "mediaType")
		}
		if data := str(x, "data"); data != "" {
			if strings.HasPrefix(data, "data:") {
				return attachmentReference{MediaType: media, Data: data}, true
			}
			return attachmentReference{MediaType: media, Data: "data:" + media + ";base64," + data}, true
		}
		for _, k := range []string{"path", "file_path", "filename", "image_path"} {
			if p := str(x, k); filepath.IsAbs(p) {
				return attachmentReference{MediaType: media, Path: p}, true
			}
		}
	}
	return attachmentReference{}, false
}
func claudeAttachmentReferences(record map[string]any) []attachmentReference {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	var out []attachmentReference
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		switch str(b, "type") {
		case "image":
			if r, ok := attachmentReferenceFromValue(b["source"]); ok {
				out = append(out, r)
			}
		case "tool_result":
			content, _ := b["content"].([]any)
			for _, v := range content {
				if r, ok := attachmentReferenceFromValue(v); ok {
					out = append(out, r)
				}
			}
		}
	}
	return out
}
func codexAttachmentReferences(record map[string]any) []attachmentReference {
	payload, _ := record["payload"].(map[string]any)
	var out []attachmentReference
	if images, ok := payload["images"].([]any); ok {
		for _, v := range images {
			if r, ok := attachmentReferenceFromValue(v); ok {
				out = append(out, r)
			}
		}
	}
	blocks, _ := payload["content"].([]any)
	for _, v := range blocks {
		b, _ := v.(map[string]any)
		if strings.Contains(str(b, "type"), "image") {
			if r, ok := attachmentReferenceFromValue(b); ok {
				out = append(out, r)
			}
		}
	}
	return out
}
func timelineItemsForSource(record map[string]any, source string) []surface.TimelineItem {
	if source == "claude" {
		return claudeTimelineItems(record)
	}
	return codexTimelineItems(record)
}
func attachmentReferencesForSource(record map[string]any, source string) []attachmentReference {
	if source == "claude" {
		return claudeAttachmentReferences(record)
	}
	return codexAttachmentReferences(record)
}
func decorateTimelineAttachments(ctx context.Context, items []surface.TimelineItem, record map[string]any, source string, offset int64) error {
	refs := attachmentReferencesForSource(record, source)
	refIndex := 0
	for itemIndex := range items {
		if items[itemIndex].Kind != "attachment" {
			continue
		}
		if refIndex >= len(refs) {
			break
		}
		ref := refs[refIndex]
		refIndex++
		data, err := readAttachmentReference(ctx, ref)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		media, width, height, err := validateAttachment(data)
		if err != nil {
			continue
		}
		items[itemIndex].Attachment = &surface.Attachment{ID: attachmentID(offset, itemIndex, data), MediaType: media, Width: width, Height: height, Bytes: int64(len(data))}
		items[itemIndex].Text = "Image attachment"
	}
	return nil
}
func AttachmentHTTPStatus(err error) (int, string, string) {
	if errors.Is(err, ErrAttachmentTooLarge) {
		return http.StatusRequestEntityTooLarge, "attachment_too_large", "The attachment exceeds the 10 MiB limit."
	}
	if errors.Is(err, ErrAttachmentInvalid) {
		return http.StatusUnsupportedMediaType, "attachment_invalid", "The referenced attachment is not a supported image."
	}
	return http.StatusNotFound, "attachment_not_found", "The referenced attachment was not found."
}
