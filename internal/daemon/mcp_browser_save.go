package daemon

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func screenshotPath(caller *mcpCaller, path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !strings.EqualFold(filepath.Ext(path), ".png") {
		return "", refuse("invalid", "path must be an absolute, clean path to a .png file.")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", refuse("invalid", "path's folder must already exist.")
	}
	ceiling, err := parseCeiling(caller.ceiling)
	if err != nil {
		return "", err
	}
	roots := ceiling.Roots
	if len(roots) == 0 && caller.session.Cwd != "" {
		roots = []string{caller.session.Cwd}
	}
	real := make([]string, 0, len(roots))
	for _, root := range roots {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			real = append(real, resolved)
		}
	}
	if !underRoot(parent, real) {
		return "", &mcpRefusal{Code: "out_of_scope", Message: "That path is outside the folders you may work in.", Detail: map[string]any{"roots": roots}}
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func saveScreenshot(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return refuse("exists", "A file is already at that path; choose a new one.")
	}
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		os.Remove(to)
		return err
	}
	if err := target.Close(); err != nil {
		os.Remove(to)
		return err
	}
	return nil
}
