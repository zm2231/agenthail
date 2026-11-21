package transcript

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

type sentFileFacts struct {
	hash, mediaType string
	width, height   int
	bytes           int64
	image           bool
}

// The recorded type is used only where the bytes sniff as generic; Claude
// records null for some files, and the extension is never trusted.
func describeSentFile(ref AttachmentReference, data []byte) sentFileFacts {
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

func sentFileFactsFor(ctx context.Context, ref AttachmentReference) (sentFileFacts, error) {
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

func DecorateSentFile(ctx context.Context, item *surface.TimelineItem, ref AttachmentReference, offset int64, index int) error {
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
	id := AttachmentIDFromHash(offset, index, facts.hash)
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

func readSentFile(ctx context.Context, ref AttachmentReference, expected string) (*surface.Attachment, []byte, error) {
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
