package surface

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"path/filepath"
	"strings"
)

const attachedFilePrefix = "Attached file: "

// Upload is a file a person attached to a message. The daemon that owns the
// target session stored it at Path, so Path is readable by that session's
// agent; clients only ever hold the ID.
type Upload struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Bytes     int64  `json:"bytes"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// IsImage reports whether the upload is a validated raster image a runtime
// can take as image input.
func (u Upload) IsImage() bool { return IsImageMediaType(u.MediaType) }

func IsImageMediaType(media string) bool {
	return media == "image/png" || media == "image/jpeg" || media == "image/gif"
}

// DetectImage sniffs PNG, JPEG or GIF bytes and decodes the header. Anything
// else, including SVG and malformed images, is not an image.
func DetectImage(data []byte) (media string, width, height int, ok bool) {
	media = http.DetectContentType(data)
	if !IsImageMediaType(media) {
		return "", 0, 0, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, false
	}
	return media, config.Width, config.Height, true
}

type Uploads []Upload

func (u Uploads) Images() Uploads {
	var images Uploads
	for _, upload := range u {
		if upload.IsImage() {
			images = append(images, upload)
		}
	}
	return images
}

// Compose returns the message a runtime receives: the typed text followed by
// one line per upload it does not take as image input, naming the stored
// path. With native false every upload is named, images included.
func (u Uploads) Compose(message string, native bool) (string, Uploads) {
	var images Uploads
	var lines []string
	for _, upload := range u {
		if native && upload.IsImage() {
			images = append(images, upload)
			continue
		}
		lines = append(lines, attachedFilePrefix+upload.Path)
	}
	if len(lines) == 0 {
		return message, images
	}
	text := strings.TrimSpace(message)
	if text != "" {
		text += "\n\n"
	}
	return text + strings.Join(lines, "\n"), images
}

// SplitAttachedFiles undoes Compose for display: it returns the typed text
// and the files named by the trailing "Attached file:" lines. Text without
// such a block, or with one that is not separated from the typed text by a
// blank line, is returned unchanged.
func SplitAttachedFiles(text string) (string, []AttachedFile) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	start := len(lines)
	for start > 0 {
		path, ok := strings.CutPrefix(lines[start-1], attachedFilePrefix)
		if !ok || !filepath.IsAbs(path) {
			break
		}
		start--
	}
	if start == len(lines) || (start > 0 && lines[start-1] != "") {
		return text, nil
	}
	files := make([]AttachedFile, 0, len(lines)-start)
	for _, line := range lines[start:] {
		path := strings.TrimPrefix(line, attachedFilePrefix)
		files = append(files, AttachedFile{Name: filepath.Base(path), Path: path})
	}
	return strings.TrimRight(strings.Join(lines[:start], "\n"), "\n"), files
}

func (u Uploads) Value() (driver.Value, error) {
	if u == nil {
		u = Uploads{}
	}
	data, err := json.Marshal(u)
	return string(data), err
}

func (u *Uploads) Scan(value any) error {
	var data []byte
	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("invalid stored uploads: %T", value)
	}
	*u = nil
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, u)
}
