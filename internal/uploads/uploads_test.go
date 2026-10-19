package uploads

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

var onePixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

type countingReader struct {
	reader io.Reader
	read   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.read += int64(n)
	return n, err
}

func TestOversizedImageIsRefusedWithoutReadingTheWholeBody(t *testing.T) {
	store := New(t.TempDir())
	body := &countingReader{reader: io.MultiReader(bytes.NewReader(onePixelPNG), bytes.NewReader(make([]byte, 20<<20)))}
	if _, err := store.Put("large.png", body); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if body.read > MaxImageBytes+64<<10 {
		t.Fatalf("read %d bytes of an oversized image", body.read)
	}
	document := bytes.Repeat([]byte("synthetic notes\n"), int(MaxImageBytes/16)+1024)
	if upload, err := store.Put("notes.txt", bytes.NewReader(document)); err != nil || upload.Bytes != int64(len(document)) {
		t.Fatalf("a document past the image limit: upload=%+v err=%v", upload, err)
	}
}

func TestResolvedUploadOutlivesTheSweepThatFollows(t *testing.T) {
	store := New(t.TempDir())
	clock := time.Now()
	store.now = func() time.Time { return clock }
	stored, err := store.Put("shot.png", bytes.NewReader(onePixelPNG))
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(Retention + time.Hour)
	resolved, err := store.Resolve(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(resolved.Path); err != nil {
		t.Fatalf("a just-resolved upload was swept: %v", err)
	}
	clock = clock.Add(Retention + time.Hour)
	if err := store.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve(stored.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an upload unused for a full retention survived: %v", err)
	}
}
