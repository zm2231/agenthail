package simhost

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	// AppArchiveLimit bounds an uploaded app's files, and separately its
	// compressed stream; a debug build of a small app is under 100 MiB.
	AppArchiveLimit   = 512 << 20
	appArchiveEntries = 50000
)

func tooLargeApp() *Error {
	return refuse("too_large", "The app is over %d MiB.", AppArchiveLimit>>20)
}

func walkApp(fsys fs.FS, from string, visit func(name string, entry fs.DirEntry) error) error {
	return fs.WalkDir(fsys, from, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			return refuse("invalid", "The app holds a link (%s); install takes apps without links.", path.Base(name))
		case entry.IsDir(), entry.Type().IsRegular():
			return visit(name, entry)
		default:
			return refuse("invalid", "The app holds something that is neither a file nor a folder (%s).", path.Base(name))
		}
	})
}

type AppBundle struct {
	root *os.Root
	name string
	size int64
}

func OpenApp(app string) (*AppBundle, error) {
	if !filepath.IsAbs(app) || filepath.Ext(filepath.Clean(app)) != ".app" {
		return nil, refuse("invalid", "app must be the absolute path of a .app.")
	}
	info, err := os.Lstat(app)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refuse("not_found", "No app is at %s.", app)
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, refuse("invalid", "app is a link; give the .app folder itself.")
	}
	if !info.IsDir() {
		return nil, refuse("invalid", "app must be a .app folder.")
	}
	root, err := os.OpenRoot(app)
	if err != nil {
		return nil, err
	}
	bundle := &AppBundle{root: root, name: filepath.Base(filepath.Clean(app))}
	entries := 0
	err = walkApp(root.FS(), ".", func(_ string, entry fs.DirEntry) error {
		if entries++; entries > appArchiveEntries {
			return tooLargeApp()
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if bundle.size += info.Size(); bundle.size > AppArchiveLimit {
			return tooLargeApp()
		}
		return nil
	})
	if err != nil {
		root.Close()
		return nil, err
	}
	return bundle, nil
}

func (b *AppBundle) Close() error { return b.root.Close() }

func (b *AppBundle) WriteArchive(w io.Writer) error {
	compressed := gzip.NewWriter(w)
	archive := tar.NewWriter(compressed)
	fsys := b.root.FS()
	var total int64
	err := walkApp(fsys, ".", func(name string, entry fs.DirEntry) error {
		target := path.Join(b.name, name)
		if entry.IsDir() {
			return archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: target + "/", Mode: 0o755})
		}
		file, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s changed while it was sent", name)
		}
		if total += info.Size(); total > AppArchiveLimit {
			return tooLargeApp()
		}
		if err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: target, Mode: int64(info.Mode().Perm()), Size: info.Size()}); err != nil {
			return err
		}
		_, err = io.CopyN(archive, file, info.Size())
		return err
	})
	if err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return compressed.Close()
}

// InstallArchive checks the lease before archive is first read, so a caller
// that sends Expect: 100-continue sends nothing when it is refused.
func (h *Host) InstallArchive(ctx context.Context, owner string, req Request, archive io.Reader) (map[string]any, error) {
	l, err := h.beginUpload(owner, req.Kind, req.Runtime)
	if err != nil {
		return nil, err
	}
	defer h.endUpload(l)
	stage, err := h.stagingDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	root, err := os.OpenRoot(stage)
	if err != nil {
		return nil, err
	}
	name, err := extractApp(root, &cappedReader{r: archive, left: AppArchiveLimit})
	root.Close()
	if err != nil {
		return nil, err
	}
	if again, err := h.held(owner, req.Kind, req.Runtime); err != nil || again != l {
		return nil, refuse("not_held", "Your lease of that simulator ended during the upload; acquire it again.")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return nil, refuse("not_held", "Your lease of that simulator ended during the upload; acquire it again.")
	}
	if _, err := h.run(ctx, "xcrun", "simctl", "install", l.udid, filepath.Join(stage, name)); err != nil {
		return nil, err
	}
	return map[string]any{"installed": name}, nil
}

func (h *Host) beginUpload(owner, kind, runtime string) (*lease, error) {
	l, err := h.held(owner, kind, runtime)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if l.uploading {
		return nil, refuse("busy", "An app is already uploading to that simulator; wait for it to finish.")
	}
	l.uploading = true
	return l, nil
}

func (h *Host) endUpload(l *lease) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l.uploading = false
}

func (h *Host) stagingDir() (string, error) {
	if err := os.MkdirAll(filepath.Join(h.outRoot, ".staging"), 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Join(h.outRoot, ".staging"), "install-")
}

func (h *Host) sweepStaging() {
	dir := filepath.Join(h.outRoot, ".staging")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "install-") {
			_ = os.RemoveAll(filepath.Join(dir, entry.Name()))
		}
	}
}

type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, tooLargeApp()
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

func extractApp(root *os.Root, archive io.Reader) (string, error) {
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", archiveError(err)
	}
	reader := tar.NewReader(compressed)
	var app string
	var total int64
	entries := 0
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", archiveError(err)
		}
		if entries++; entries > appArchiveEntries {
			return "", tooLargeApp()
		}
		raw := strings.TrimSuffix(header.Name, "/")
		name := path.Clean(raw)
		if raw == "" || name != raw || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, `\`) {
			return "", refuse("invalid", "The upload names an unsafe path %q.", header.Name)
		}
		top, _, _ := strings.Cut(name, "/")
		if app == "" {
			if path.Ext(top) != ".app" {
				return "", refuse("invalid", "The upload must hold one .app folder.")
			}
			app = top
		} else if top != app {
			return "", refuse("invalid", "The upload must hold one .app folder.")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o700); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if header.Size < 0 || header.Size > AppArchiveLimit-total {
				return "", tooLargeApp()
			}
			if err := root.MkdirAll(path.Dir(name), 0o700); err != nil {
				return "", err
			}
			file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(header.Mode).Perm()&0o755|0o600)
			if err != nil {
				return "", err
			}
			written, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return "", archiveError(copyErr)
			}
			if closeErr != nil {
				return "", closeErr
			}
			total += written
		default:
			return "", refuse("invalid", "The upload holds %q, which is neither a file nor a folder.", header.Name)
		}
	}
	if app == "" {
		return "", refuse("invalid", "The upload must hold one .app folder.")
	}
	if info, err := root.Lstat(app); err != nil || !info.IsDir() {
		return "", refuse("invalid", "The upload must hold one .app folder.")
	}
	return app, nil
}

func archiveError(err error) error {
	var refusal *Error
	if errors.As(err, &refusal) {
		return refusal
	}
	return refuse("invalid", "The upload is not a readable app archive: %v.", err)
}
