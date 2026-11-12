package simhost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const ownerOutLimit = 64 << 20

func (h *Host) ownerDir(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return filepath.Join(h.outRoot, hex.EncodeToString(sum[:8]))
}

func (h *Host) outFile(owner, name string) (string, error) {
	dir := h.ownerDir(owner)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d-%s", h.now().UnixNano(), name)), nil
}

func (h *Host) writeOut(owner, name string, data []byte) (string, error) {
	if len(data) > ownerOutLimit {
		return "", refuse("too_large", "The result is over %d MiB.", ownerOutLimit>>20)
	}
	path, err := h.outFile(owner, name)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	h.prune(owner, path)
	return path, nil
}

// prune keeps an owner's plain files under the limit, oldest first; folders such as a copied .app are the owner's to manage.
func (h *Host) prune(owner, keep string) {
	entries, err := os.ReadDir(h.ownerDir(owner))
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  int64
	}
	var files []file
	var total int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(h.ownerDir(owner), entry.Name())
		files = append(files, file{path, info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for _, f := range files {
		if total <= ownerOutLimit {
			return
		}
		if f.path == keep {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// RemoveIdleOwner leaves the folder of an owner that has since taken a new lease, such as a session restored from archive.
func (h *Host) RemoveIdleOwner(owner string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ownedKinds(owner) == 0 {
		_ = os.RemoveAll(h.ownerDir(owner))
	}
}
