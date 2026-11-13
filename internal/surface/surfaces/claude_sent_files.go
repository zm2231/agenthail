package surfaces

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const MaxSentFileBytes int64 = 50 << 20

var ErrSentFileTooLarge = fmt.Errorf("%w: a sent file exceeds 50 MiB", ErrAttachmentTooLarge)

type claudeSentFile struct {
	path, mediaType string
	image           bool
}

func claudeSentFiles(record map[string]any) (callID, caption string, files []claudeSentFile) {
	if str(record, "type") != "user" {
		return "", "", nil
	}
	result, _ := record["toolUseResult"].(map[string]any)
	entries, _ := result["attachments"].([]any)
	if callID = claudeToolResultID(record); callID == "" || len(entries) == 0 {
		return "", "", nil
	}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path := str(entry, "path")
		_, sized := entry["size"].(float64)
		image, hinted := entry["isImage"].(bool)
		if !filepath.IsAbs(path) || !sized || !hinted {
			return "", "", nil
		}
		files = append(files, claudeSentFile{path: path, mediaType: str(entry, "media_type"), image: image})
	}
	return callID, str(result, "caption"), files
}

func claudeToolResultID(record map[string]any) string {
	message, _ := record["message"].(map[string]any)
	if id := str(message, "tool_use_id"); id != "" {
		return id
	}
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		if block, _ := raw.(map[string]any); str(block, "type") == "tool_result" {
			return str(block, "tool_use_id")
		}
	}
	return ""
}

// verifiedSentFiles returns a result record's files only when its call is a
// SendUserFile tool_use earlier in the transcript: any tool's result can
// carry a toolUseResult of the same shape, and its paths must never be served.
func verifiedSentFiles(ctx context.Context, calls *claudeToolCalls, record map[string]any, offset int64) (string, []claudeSentFile, error) {
	callID, caption, files := claudeSentFiles(record)
	if len(files) == 0 {
		return "", nil, nil
	}
	name, err := calls.name(ctx, callID, offset)
	if err != nil || name != "SendUserFile" {
		return "", nil, err
	}
	return caption, files, nil
}

func sentFileReference(file claudeSentFile) attachmentReference {
	return attachmentReference{MediaType: file.mediaType, Path: file.path, Sent: true, Image: file.image}
}

func appendSentFiles(ctx context.Context, items []surface.TimelineItem, calls *claudeToolCalls, record map[string]any, offset int64, line []byte) ([]surface.TimelineItem, error) {
	caption, files, err := verifiedSentFiles(ctx, calls, record, offset)
	if err != nil || len(files) == 0 {
		return items, err
	}
	callID := claudeToolResultID(record)
	for i, file := range files {
		index := len(items)
		name := filepath.Base(file.path)
		item := surface.TimelineItem{ID: stableTimelineItemID(offset, line, index) + "-file", Kind: "file", Role: "assistant", Title: name, CallID: callID, Files: []surface.AttachedFile{{Name: name, Path: file.path}}}
		if i == 0 {
			item.Text = caption
		}
		if err := decorateSentFile(ctx, &item, sentFileReference(file), offset, index); err != nil {
			return items, err
		}
		items = append(items, item)
	}
	return items, nil
}

type sentFileFacts struct {
	hash, mediaType string
	width, height   int
	bytes           int64
	image           bool
}

// The recorded type is used only where the bytes sniff as generic; Claude
// records null for some files, and the extension is never trusted.
func describeSentFile(ref attachmentReference, data []byte) sentFileFacts {
	sum := sha256.Sum256(data)
	facts := sentFileFacts{hash: hex.EncodeToString(sum[:]), bytes: int64(len(data))}
	if ref.Image {
		if media, width, height, err := validateAttachment(data); err == nil {
			facts.mediaType, facts.width, facts.height, facts.image = media, width, height, true
			return facts
		}
	}
	facts.mediaType = http.DetectContentType(data)
	generic := facts.mediaType == "application/octet-stream" || strings.HasPrefix(facts.mediaType, "text/plain")
	if generic && ref.MediaType != "" {
		facts.mediaType = ref.MediaType
	}
	return facts
}

type sentFileKey struct {
	path     string
	size     int64
	modified time.Time
}

// Serving hashes the bytes it sends; only decoration trusts this cache.
var sentFileCache = struct {
	sync.Mutex
	facts map[sentFileKey]sentFileFacts
}{facts: map[sentFileKey]sentFileFacts{}}

const sentFileCacheEntries = 256

func sentFileFactsFor(ctx context.Context, ref attachmentReference) (sentFileFacts, error) {
	info, err := os.Lstat(ref.Path)
	if err != nil || !info.Mode().IsRegular() {
		return sentFileFacts{}, ErrAttachmentNotFound
	}
	if info.Size() > MaxSentFileBytes {
		return sentFileFacts{}, ErrSentFileTooLarge
	}
	key := sentFileKey{path: ref.Path, size: info.Size(), modified: info.ModTime()}
	sentFileCache.Lock()
	facts, ok := sentFileCache.facts[key]
	sentFileCache.Unlock()
	if ok && facts.image == ref.Image {
		return facts, nil
	}
	data, err := readLocalFile(ctx, ref.Path, MaxSentFileBytes)
	if err != nil {
		return sentFileFacts{}, err
	}
	facts = describeSentFile(ref, data)
	sentFileCache.Lock()
	if len(sentFileCache.facts) >= sentFileCacheEntries {
		clear(sentFileCache.facts)
	}
	sentFileCache.facts[key] = facts
	sentFileCache.Unlock()
	return facts, nil
}

func decorateSentFile(ctx context.Context, item *surface.TimelineItem, ref attachmentReference, offset int64, index int) error {
	if info, err := os.Lstat(ref.Path); err == nil && info.Mode().IsRegular() && len(item.Files) == 1 {
		item.Files[0].Bytes = info.Size()
	}
	facts, err := sentFileFactsFor(ctx, ref)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	id := attachmentIDFromHash(offset, index, facts.hash)
	if facts.image {
		caption := item.Text
		if caption == "" {
			caption = "Image attachment"
		}
		*item = surface.TimelineItem{ID: item.ID, Kind: "attachment", Role: item.Role, Title: "Image", Text: caption, CallID: item.CallID,
			Attachment: &surface.Attachment{ID: id, MediaType: facts.mediaType, Width: facts.width, Height: facts.height, Bytes: facts.bytes}}
		return nil
	}
	item.Attachment = &surface.Attachment{ID: id, MediaType: facts.mediaType, Bytes: facts.bytes, Name: item.Title}
	return nil
}

func readSentFile(ctx context.Context, ref attachmentReference, expected string) (*surface.Attachment, []byte, error) {
	data, err := readLocalFile(ctx, ref.Path, MaxSentFileBytes)
	if errors.Is(err, ErrAttachmentTooLarge) {
		return nil, nil, ErrSentFileTooLarge
	}
	if err != nil {
		return nil, nil, err
	}
	facts := describeSentFile(ref, data)
	if facts.hash != expected {
		return nil, nil, ErrAttachmentNotFound
	}
	attachment := &surface.Attachment{MediaType: facts.mediaType, Width: facts.width, Height: facts.height, Bytes: facts.bytes}
	if !facts.image {
		attachment.Name = filepath.Base(ref.Path)
	}
	return attachment, data, nil
}
