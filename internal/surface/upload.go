package surface

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
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
	if media == "image/jpeg" && jpegOrientation(data) >= 5 {
		return media, config.Height, config.Width, true
	}
	return media, config.Width, config.Height, true
}

// EXIF orientations 5 to 8 turn the picture a quarter, so it shows with width and height swapped.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for at := 2; at+4 <= len(data); {
		if data[at] != 0xFF {
			return 1
		}
		if data[at+1] == 0xFF {
			at++
			continue
		}
		marker := data[at+1]
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		size := int(data[at+2])<<8 | int(data[at+3])
		if size < 2 || at+2+size > len(data) {
			return 1
		}
		segment := data[at+4 : at+2+size]
		if marker == 0xE1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return tiffOrientation(segment[6:])
		}
		at += 2 + size
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	for i := 0; i < count; i++ {
		entry := ifd + 2 + i*12
		if entry+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[entry:entry+2]) == 0x0112 {
			if order.Uint16(tiff[entry+2:entry+4]) != 3 || order.Uint32(tiff[entry+4:entry+8]) != 1 {
				return 1
			}
			value := int(order.Uint16(tiff[entry+8 : entry+10]))
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
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
	data, err := storedJSON(value, "uploads")
	if err != nil {
		return err
	}
	*u = nil
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, u)
}
