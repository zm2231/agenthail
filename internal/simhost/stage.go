package simhost

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stageApp copies app out of the owner's folder through an os.Root, so no link or later swap reaches outside it, and simctl installs only the copy.
func (h *Host) stageApp(owner, app string) (string, func(), error) {
	dir := h.ownerDir(owner)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	rel, ok := relativeTo(dir, app)
	if !ok {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			rel, ok = relativeTo(real, app)
		}
	}
	if !ok {
		return "", nil, &Error{Code: "out_of_scope", Message: "Copy the .app into your outDir first; install takes apps only from there.", Detail: map[string]any{"outDir": dir}}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, refuse("not_found", "No app is at that path in your outDir.")
	}
	if err != nil {
		return "", nil, &Error{Code: "out_of_scope", Message: "That path leaves your outDir; copy the .app itself into it.", Detail: map[string]any{"outDir": dir}}
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", nil, &Error{Code: "out_of_scope", Message: "app is a link; copy the .app itself into your outDir.", Detail: map[string]any{"outDir": dir}}
	}
	if !info.IsDir() {
		return "", nil, refuse("invalid", "app must be a .app folder.")
	}
	stage, err := h.stagingDir()
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(stage) }
	target := filepath.Join(stage, filepath.Base(rel))
	if err := copyTree(root.FS(), filepath.ToSlash(rel), target); err != nil {
		cleanup()
		return "", nil, err
	}
	return target, cleanup, nil
}

func relativeTo(dir, app string) (string, bool) {
	rel, err := filepath.Rel(dir, app)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func copyTree(fsys fs.FS, from, to string) error {
	return walkApp(fsys, from, func(name string, entry fs.DirEntry) error {
		dest := filepath.Join(to, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(name, from), "/")))
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		return copyFile(fsys, name, dest)
	})
}

func copyFile(fsys fs.FS, name, dest string) error {
	source, err := fsys.Open(name)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New(name + " changed while it was copied")
	}
	target, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	return target.Close()
}
