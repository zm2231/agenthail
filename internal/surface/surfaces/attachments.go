package surfaces

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const maxAttachmentBytes int64 = 10 << 20
const attachmentTranscriptReadBudget int64 = 16 << 20

var ErrAttachmentNotFound = errors.New("attachment not found")
var ErrAttachmentTooLarge = errors.New("attachment exceeds 10 MiB")
var ErrAttachmentInvalid = errors.New("attachment is not a supported image")

type attachmentReference struct {
	MediaType string
	Data      string
	Path      string
}

func (c *Claude) ReadAttachment(ctx context.Context, session *surface.Session, id string) (*surface.Attachment, []byte, error) {
	return readTranscriptAttachment(ctx, session, id, "claude")
}

func (c *Codex) ReadAttachment(ctx context.Context, session *surface.Session, id string) (*surface.Attachment, []byte, error) {
	return readTranscriptAttachment(ctx, session, id, "codex")
}

func readTranscriptAttachment(ctx context.Context, session *surface.Session, id, source string) (*surface.Attachment, []byte, error) {
	path := session.Transcript
	if path == "" {
		if source == "claude" {
			path = NewClaude("", "").transcriptPath(session)
		} else {
			path = codexTranscriptPath(session)
		}
	}
	if path == "" {
		return nil, nil, ErrAttachmentNotFound
	}
	ref, found, err := findTranscriptAttachment(ctx, path, source, id)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		return nil, nil, ErrAttachmentNotFound
	}
	data, err := readAttachmentReference(ctx, ref, filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	mediaType, width, height, err := validateAttachment(data)
	if err != nil {
		return nil, nil, err
	}
	return &surface.Attachment{ID: id, MediaType: mediaType, Width: width, Height: height, Bytes: int64(len(data))}, data, nil
}

func readAttachmentReference(ctx context.Context, ref attachmentReference, transcriptDir string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if ref.Data != "" {
		if strings.HasPrefix(ref.Data, "data:") {
			parts := strings.SplitN(ref.Data, ",", 2)
			if len(parts) != 2 || !strings.HasSuffix(strings.ToLower(parts[0]), ";base64") {
				return nil, ErrAttachmentInvalid
			}
			if int64(len(parts[1])) > (maxAttachmentBytes*4/3)+8 {
				return nil, ErrAttachmentTooLarge
			}
			decoded, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return nil, ErrAttachmentInvalid
			}
			if int64(len(decoded)) > maxAttachmentBytes {
				return nil, ErrAttachmentTooLarge
			}
			return decoded, nil
		}
		return nil, ErrAttachmentInvalid
	}
	if ref.Path == "" {
		return nil, ErrAttachmentInvalid
	}
	resolved, err := filepath.Abs(ref.Path)
	if err != nil {
		return nil, ErrAttachmentNotFound
	}
	root, err := filepath.Abs(transcriptDir)
	if err != nil {
		return nil, ErrAttachmentNotFound
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, ErrAttachmentNotFound
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrAttachmentNotFound
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, ErrAttachmentNotFound
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil {
		return nil, ErrAttachmentNotFound
	}
	if int64(len(data)) > maxAttachmentBytes {
		return nil, ErrAttachmentTooLarge
	}
	return data, nil
}

func validateAttachment(data []byte) (string, int, int, error) {
	if len(data) == 0 {
		return "", 0, 0, ErrAttachmentInvalid
	}
	mediaType := http.DetectContentType(data)
	if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/gif" {
		return "", 0, 0, ErrAttachmentInvalid
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, ErrAttachmentInvalid
	}
	return mediaType, config.Width, config.Height, nil
}

func findTranscriptAttachment(ctx context.Context, path, source, id string) (attachmentReference, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return attachmentReference{}, false, ErrAttachmentNotFound
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return attachmentReference{}, false, ErrAttachmentNotFound
	}
	start := info.Size() - attachmentTranscriptReadBudget
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return attachmentReference{}, false, ErrAttachmentNotFound
	}
	reader := io.NewSectionReader(file, start, info.Size()-start)
	data, err := io.ReadAll(reader)
	if err != nil {
		return attachmentReference{}, false, ErrAttachmentNotFound
	}
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			start += int64(newline + 1)
			data = data[newline+1:]
		}
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	offset := start
	for _, line := range lines {
		if err := ctx.Err(); err != nil {
			return attachmentReference{}, false, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			offset += int64(len(line))
			continue
		}
		var record map[string]any
		if json.Unmarshal(line, &record) == nil {
			items := timelineItemsForSource(record, source)
			refs := attachmentReferencesForSource(record, source)
			refIndex := 0
			for itemIndex, item := range items {
				if item.Kind != "attachment" {
					continue
				}
				itemID := stableTimelineItemID(offset, line, itemIndex)
				if refIndex < len(refs) && attachmentID(itemID, refs[refIndex], filepath.Dir(path)) == id {
					if refIndex < len(refs) {
						return refs[refIndex], true, nil
					}
				}
				refIndex++
			}
		}
		offset += int64(len(line))
	}
	return attachmentReference{}, false, nil
}

func attachmentID(itemID string, ref attachmentReference, transcriptDir string) string {
	hash := sha256.New()
	if ref.Data != "" {
		_, _ = hash.Write([]byte(ref.Data))
	} else {
		path, err := filepath.Abs(ref.Path)
		root, rootErr := filepath.Abs(transcriptDir)
		if err != nil || rootErr != nil {
			_, _ = hash.Write([]byte(ref.Path))
		} else if rel, relErr := filepath.Rel(root, path); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			_, _ = hash.Write([]byte(ref.Path))
		} else if file, openErr := os.Open(path); openErr == nil {
			defer file.Close()
			_, _ = io.CopyN(hash, file, maxAttachmentBytes+1)
		} else {
			_, _ = hash.Write([]byte(ref.Path))
		}
	}
	return fmt.Sprintf("attachment:%s:%x", itemID, hash.Sum(nil))
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

func attachmentReferenceFromValue(value any) (attachmentReference, bool) {
	switch value := value.(type) {
	case string:
		if strings.HasPrefix(value, "data:") {
			return attachmentReference{Data: value}, true
		}
		if strings.HasPrefix(value, "/") {
			return attachmentReference{Path: value}, true
		}
	case map[string]any:
		if str(value, "type") == "image" {
			return attachmentReferenceFromValue(value["source"])
		}
		mediaType := str(value, "media_type")
		if mediaType == "" {
			mediaType = str(value, "mediaType")
		}
		if data := str(value, "data"); data != "" {
			if strings.HasPrefix(data, "data:") {
				return attachmentReference{MediaType: mediaType, Data: data}, true
			}
			return attachmentReference{MediaType: mediaType, Data: "data:" + mediaType + ";base64," + data}, true
		}
		for _, key := range []string{"path", "file_path", "filename", "image_path"} {
			if path := str(value, key); path != "" {
				return attachmentReference{MediaType: mediaType, Path: path}, true
			}
		}
		for _, key := range []string{"image_url", "url"} {
			if data := str(value, key); strings.HasPrefix(data, "data:") {
				return attachmentReference{MediaType: mediaType, Data: data}, true
			}
		}
	}
	return attachmentReference{}, false
}

func claudeAttachmentReferences(record map[string]any) []attachmentReference {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	refs := make([]attachmentReference, 0)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "image" {
			if ref, ok := attachmentReferenceFromValue(block["source"]); ok {
				refs = append(refs, ref)
			}
		}
		if str(block, "type") == "tool_result" {
			content, _ := block["content"].([]any)
			for _, entry := range content {
				if ref, ok := attachmentReferenceFromValue(entry); ok {
					refs = append(refs, ref)
				}
			}
		}
	}
	return refs
}

func codexAttachmentReferences(record map[string]any) []attachmentReference {
	payload, _ := record["payload"].(map[string]any)
	refs := make([]attachmentReference, 0)
	if images, ok := payload["images"].([]any); ok {
		for _, image := range images {
			if ref, ok := attachmentReferenceFromValue(image); ok {
				refs = append(refs, ref)
			}
		}
	}
	blocks, _ := payload["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if strings.Contains(str(block, "type"), "image") {
			if ref, ok := attachmentReferenceFromValue(block); ok {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func AttachmentHTTPStatus(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrAttachmentTooLarge):
		return http.StatusRequestEntityTooLarge, "attachment_too_large", "The attachment exceeds the 10 MiB limit."
	case errors.Is(err, ErrAttachmentInvalid):
		return http.StatusUnsupportedMediaType, "attachment_invalid", "The referenced attachment is not a supported image."
	default:
		return http.StatusNotFound, "attachment_not_found", "The referenced attachment was not found."
	}
}
