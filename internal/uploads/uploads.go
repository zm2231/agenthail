package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	MaxFileBytes  int64 = 25 << 20
	MaxImageBytes int64 = 5 << 20
	Retention           = 7 * 24 * time.Hour
	idPrefix            = "upload:"
	maxNameLength       = 120
)

var (
	ErrNotFound = errors.New("upload not found")
	ErrTooLarge = errors.New("upload is too large")
	ErrEmpty    = errors.New("upload is empty")
)

// Store keeps uploaded files content-addressed under one directory:
// <dir>/<sha256>/<name>. The id names both parts, so the same bytes under the
// same name are one upload and a retried upload returns the same id.
type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

func New(dir string) *Store { return &Store{dir: dir, now: time.Now} }

// Put stores the body under name. Images (sniffed PNG, JPEG or GIF with a
// decodable header) are limited to MaxImageBytes because that is the largest
// image every runtime accepts; any other file to MaxFileBytes.
func (s *Store) Put(name string, body io.Reader) (surface.Upload, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return surface.Upload{}, err
	}
	temp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return surface.Upload{}, err
	}
	defer os.Remove(temp.Name())
	hash := sha256.New()
	sink := io.MultiWriter(temp, hash)
	limited := io.LimitReader(body, MaxFileBytes+1)
	written, err := io.CopyN(sink, limited, MaxImageBytes+1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err == nil && written > MaxImageBytes {
		head, headErr := readHead(temp.Name(), written)
		if headErr != nil {
			err = headErr
		} else if _, _, _, image := surface.DetectImage(head); image {
			err = ErrTooLarge
		} else {
			var rest int64
			rest, err = io.Copy(sink, limited)
			written += rest
		}
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return surface.Upload{}, err
	}
	if written == 0 {
		return surface.Upload{}, ErrEmpty
	}
	if written > MaxFileBytes {
		return surface.Upload{}, ErrTooLarge
	}
	head, err := readHead(temp.Name(), written)
	if err != nil {
		return surface.Upload{}, err
	}
	media, width, height, image := surface.DetectImage(head)
	if image && written > MaxImageBytes {
		return surface.Upload{}, ErrTooLarge
	}
	if !image {
		media = http.DetectContentType(head)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	name = sanitizeName(name, media)
	s.mu.Lock()
	defer s.mu.Unlock()
	folder := filepath.Join(s.dir, digest)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return surface.Upload{}, err
	}
	path := filepath.Join(folder, name)
	if err := os.Rename(temp.Name(), path); err != nil {
		return surface.Upload{}, err
	}
	now := s.now()
	_ = os.Chtimes(folder, now, now)
	return surface.Upload{ID: idPrefix + digest + "/" + name, Name: name, Path: path, MediaType: media, Bytes: written, Width: width, Height: height}, nil
}

// readHead reads enough of an image to decode its header; image.DecodeConfig
// for JPEG can need more than the 512 bytes content sniffing uses.
func readHead(path string, size int64) ([]byte, error) {
	limit := min(size, MaxImageBytes)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	head := make([]byte, limit)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return head[:n], nil
}

// Resolve returns the stored upload an id names and restarts its retention, so
// a send or queued send never hands a runtime a path Sweep is about to remove.
func (s *Store) Resolve(id string) (surface.Upload, error) {
	rest, ok := strings.CutPrefix(id, idPrefix)
	if !ok {
		return surface.Upload{}, ErrNotFound
	}
	digest, name, ok := strings.Cut(rest, "/")
	if !ok || len(digest) != sha256.Size*2 || name != sanitizeName(name, "") {
		return surface.Upload{}, ErrNotFound
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return surface.Upload{}, ErrNotFound
	}
	folder := filepath.Join(s.dir, digest)
	path := filepath.Join(folder, name)
	s.mu.Lock()
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		now := s.now()
		err = os.Chtimes(folder, now, now)
	}
	s.mu.Unlock()
	if err != nil || !info.Mode().IsRegular() {
		return surface.Upload{}, ErrNotFound
	}
	head, err := readHead(path, info.Size())
	if err != nil {
		return surface.Upload{}, ErrNotFound
	}
	media, width, height, image := surface.DetectImage(head)
	if !image {
		media = http.DetectContentType(head)
	}
	return surface.Upload{ID: id, Name: name, Path: path, MediaType: media, Bytes: info.Size(), Width: width, Height: height}, nil
}

// Sweep removes uploads stored or last resolved longer ago than Retention. A
// re-upload of the same bytes also refreshes the folder's time.
func (s *Store) Sweep() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := s.now().Add(-Retention)
	var failures []error
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		stale := info.ModTime().Before(cutoff)
		if strings.HasPrefix(entry.Name(), ".incoming-") {
			stale = info.ModTime().Before(s.now().Add(-time.Hour))
		}
		if stale {
			if err := os.RemoveAll(filepath.Join(s.dir, entry.Name())); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// sanitizeName keeps a file name safe to use as one path element and to show
// an agent: letters, digits, dot, dash and underscore, no leading dot. An
// image keeps an extension matching its sniffed type.
func sanitizeName(name, media string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	clean := strings.TrimLeft(b.String(), ".")
	if len(clean) > maxNameLength {
		clean = clean[len(clean)-maxNameLength:]
		clean = strings.TrimLeft(clean, ".")
	}
	if ext := imageExtension(media); ext != "" {
		base := strings.TrimSuffix(clean, filepath.Ext(clean))
		if base == "" {
			base = "image"
		}
		return base + ext
	}
	if clean == "" {
		return "file"
	}
	return clean
}

func imageExtension(media string) string {
	switch media {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	}
	return ""
}
