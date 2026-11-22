package reader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotes(t *testing.T) {
	if _, err := os.ReadFile(filepath.Join("..", "data", "notes.txt")); err != nil {
		t.Fatal(err)
	}
}
