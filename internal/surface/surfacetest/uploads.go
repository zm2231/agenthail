package surfacetest

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func StoredUploads(t *testing.T) (surface.Upload, surface.Upload) {
	t.Helper()
	dir := t.TempDir()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	imagePath, notesPath := filepath.Join(dir, "shot.png"), filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(imagePath, png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notesPath, []byte("synthetic notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return surface.Upload{ID: "upload:image", Name: "shot.png", Path: imagePath, MediaType: "image/png"},
		surface.Upload{ID: "upload:notes", Name: "notes.txt", Path: notesPath, MediaType: "text/plain; charset=utf-8"}
}
