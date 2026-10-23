package atomicfile

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Write(path string, data []byte, perm fs.FileMode) error {
	return write(path, bytes.NewReader(data), perm, false)
}

// WriteSynced also flushes the content to disk before the rename, so the new
// file survives a crash, not only a concurrent reader.
func WriteSynced(path string, data []byte, perm fs.FileMode) error {
	return write(path, bytes.NewReader(data), perm, true)
}

func WriteFrom(path string, r io.Reader, perm fs.FileMode) error {
	return write(path, r, perm, false)
}

func write(path string, r io.Reader, perm fs.FileMode, sync bool) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(perm); err != nil {
		temp.Close()
		return err
	}
	if _, err := io.Copy(temp, r); err != nil {
		temp.Close()
		return err
	}
	if sync {
		if err := temp.Sync(); err != nil {
			temp.Close()
			return err
		}
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
