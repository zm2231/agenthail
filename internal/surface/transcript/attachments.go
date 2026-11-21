package transcript

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type AttachmentReference struct {
	MediaType, Data, Path string
	Sent, Image           bool
}

func ReadAttachment(ctx context.Context, s *surface.Session, id string, format Format, transcriptPath func(*surface.Session) string) (*surface.Attachment, []byte, error) {
	path := s.Transcript
	if path == "" {
		path = transcriptPath(s)
	}
	offset, index, expected, err := parseAttachmentID(id)
	if err != nil || path == "" {
		return nil, nil, ErrAttachmentNotFound
	}
	ref, ok, err := readAttachmentRecord(ctx, path, format, offset, index)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, ErrAttachmentNotFound
	}
	if ref.Sent {
		attachment, data, err := readSentFile(ctx, ref, expected)
		if err != nil {
			return nil, nil, err
		}
		attachment.ID = id
		return attachment, data, nil
	}
	data, err := readAttachmentReference(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if HashBytes(data) != expected {
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

func readAttachmentRecord(ctx context.Context, path string, format Format, offset int64, index int) (AttachmentReference, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return AttachmentReference{}, false, err
	}
	defer f.Close()
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return AttachmentReference{}, false, err
	}
	if offset > 0 {
		var previous [1]byte
		if _, err = f.ReadAt(previous[:], offset-1); err != nil || previous[0] != '\n' {
			return AttachmentReference{}, false, nil
		}
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			return AttachmentReference{}, false, err
		}
	}
	r := bufio.NewReaderSize(f, 64*1024)
	var line []byte
	for {
		if err := ctx.Err(); err != nil {
			return AttachmentReference{}, false, err
		}
		part, e := r.ReadSlice('\n')
		if int64(len(line))+int64(len(part)) > maxAttachmentRecordBytes {
			return AttachmentReference{}, false, ErrAttachmentTooLarge
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
		return AttachmentReference{}, false, e
	}
	if err := ctx.Err(); err != nil {
		return AttachmentReference{}, false, err
	}
	var record map[string]any
	if json.Unmarshal(line, &record) != nil {
		return AttachmentReference{}, false, nil
	}
	items := format.RecordItems(record)
	refs := format.AttachmentReferences(record)
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
	if index >= len(items) {
		return format.TrailingAttachment(ctx, path, record, offset, index-len(items))
	}
	return AttachmentReference{}, false, nil
}

func readAttachmentReference(ctx context.Context, ref AttachmentReference) ([]byte, error) {
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
	return readLocalFile(ctx, ref.Path, maxAttachmentBytes)
}

func readLocalFile(ctx context.Context, path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrAttachmentNotFound
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrAttachmentNotFound
	}
	f, err := os.Open(path)
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
			if int64(len(data)+read) > limit {
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
	media, width, height, ok := surface.DetectImage(data)
	if !ok {
		return "", 0, 0, ErrAttachmentInvalid
	}
	return media, width, height, nil
}
func HashBytes(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func AttachmentID(offset int64, index int, data []byte) string {
	return AttachmentIDFromHash(offset, index, HashBytes(data))
}
func AttachmentIDFromHash(offset int64, index int, hash string) string {
	return "attachment:" + strconv.FormatInt(offset, 10) + ":" + strconv.Itoa(index) + ":" + hash
}

func AttachmentReferenceFromValue(v any) (AttachmentReference, bool) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "data:") {
			return AttachmentReference{Data: x}, true
		}
		if filepath.IsAbs(x) {
			return AttachmentReference{Path: x}, true
		}
	case map[string]any:
		if String(x, "type") == "image" {
			return AttachmentReferenceFromValue(x["source"])
		}
		media := String(x, "media_type")
		if media == "" {
			media = String(x, "mediaType")
		}
		switch imageURL := x["image_url"].(type) {
		case string:
			return AttachmentReferenceFromValue(imageURL)
		case map[string]any:
			return AttachmentReferenceFromValue(String(imageURL, "url"))
		}
		if data := String(x, "data"); data != "" {
			if strings.HasPrefix(data, "data:") {
				return AttachmentReference{MediaType: media, Data: data}, true
			}
			return AttachmentReference{MediaType: media, Data: "data:" + media + ";base64," + data}, true
		}
		for _, k := range []string{"path", "file_path", "filename", "image_path"} {
			if p := String(x, k); filepath.IsAbs(p) {
				return AttachmentReference{MediaType: media, Path: p}, true
			}
		}
	}
	return AttachmentReference{}, false
}
func AppendAttachmentReference(out []AttachmentReference, value any) []AttachmentReference {
	ref, _ := AttachmentReferenceFromValue(value)
	return append(out, ref)
}
func DecorateTimelineAttachments(ctx context.Context, items []surface.TimelineItem, refs []AttachmentReference, offset int64) error {
	for itemIndex := range items {
		item := &items[itemIndex]
		if item.Kind != "message" || item.Role != "user" {
			continue
		}
		text, files := surface.SplitAttachedFiles(item.Text)
		if len(files) == 0 {
			continue
		}
		for fileIndex := range files {
			if info, err := os.Stat(files[fileIndex].Path); err == nil && info.Mode().IsRegular() {
				files[fileIndex].Bytes = info.Size()
			}
		}
		item.Text, item.Files = text, files
	}
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
		items[itemIndex].Attachment = &surface.Attachment{ID: AttachmentID(offset, itemIndex, data), MediaType: media, Width: width, Height: height, Bytes: int64(len(data))}
		items[itemIndex].Text = "Image attachment"
	}
	return nil
}
func AttachmentHTTPStatus(err error) (int, string, string) {
	if errors.Is(err, ErrAttachmentTooLarge) {
		if errors.Is(err, ErrSentFileTooLarge) {
			return http.StatusRequestEntityTooLarge, "attachment_too_large", "The file exceeds the 50 MiB limit for sent files."
		}
		return http.StatusRequestEntityTooLarge, "attachment_too_large", "The attachment exceeds the 10 MiB limit."
	}
	if errors.Is(err, ErrAttachmentInvalid) {
		return http.StatusUnsupportedMediaType, "attachment_invalid", "The referenced attachment is not a supported image."
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return http.StatusGatewayTimeout, "attachment_timeout", "Reading the attachment took too long; try again."
	}
	return http.StatusNotFound, "attachment_not_found", "The referenced attachment was not found."
}
