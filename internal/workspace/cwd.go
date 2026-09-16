// Package workspace defines the filesystem identity rule shared by session discovery consumers.
package workspace

import (
	"path/filepath"
	"strings"
)

// NormalizeCWD returns an absolute, cleaned workspace path. Existing symlinks
// resolve to their canonical target so aliases of the same directory compare equal.
func NormalizeCWD(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	return abs, nil
}

// IsWithin reports whether candidate is the workspace root or a descendant of it.
// It compares cleaned canonical paths and never treats a common string prefix as ancestry.
func IsWithin(root, candidate string) (bool, error) {
	root, err := NormalizeCWD(root)
	if err != nil {
		return false, err
	}
	candidate, err = NormalizeCWD(candidate)
	if err != nil {
		return false, err
	}
	if root == "" || candidate == "" {
		return false, nil
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, nil
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}
