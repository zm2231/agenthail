package extension

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/gitstore"
)

const recipesDir = "recipes"

//go:embed seed
var seedFiles embed.FS

func seededPath(home string) string { return filepath.Join(DataDir(home), "seeded.json") }

// Seed writes the guide for agents that build extensions into the workspace
// and records it as Agenthail's change. A file is written when it is missing
// or when this build's copy differs from the one last written, so the
// person's own edits last until Agenthail's copy changes.
func (h *History) Seed(home string) error {
	unlock, err := h.repo.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	last := map[string]string{}
	if data, err := os.ReadFile(seededPath(home)); err == nil {
		if err := json.Unmarshal(data, &last); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next := map[string]string{}
	var written []string
	err = fs.WalkDir(seedFiles, "seed", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := seedFiles.ReadFile(name)
		if err != nil {
			return err
		}
		rel := name[len("seed/"):]
		sum := sha256.Sum256(content)
		next[rel] = hex.EncodeToString(sum[:])
		target := filepath.Join(h.repo.Dir(), filepath.FromSlash(rel))
		current, readErr := os.ReadFile(target)
		if readErr == nil && (last[rel] == next[rel] || bytes.Equal(current, content)) {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := atomicfile.Write(target, content, 0o600); err != nil {
			return err
		}
		written = append(written, path.Clean(rel))
		return nil
	})
	if err != nil {
		return err
	}
	if len(written) > 0 {
		slices.Sort(written)
		if err := h.repo.Stage(written...); err != nil {
			return err
		}
		if _, err := h.repo.CommitStaged(gitstore.Agenthail, "Update the guide for building extensions"); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(DataDir(home), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(seededPath(home), append(data, '\n'), 0o600)
}
