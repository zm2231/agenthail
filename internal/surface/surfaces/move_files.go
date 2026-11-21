package surfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
)

// movedDir holds a moved conversation's files beside, never inside,
// the folders Claude and Agenthail read conversations from.
const movedDir = "agenthail-moved"

func safeFileName(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.HasPrefix(id, ".")
}

func hashFile(name string) (int64, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	sum := sha256.New()
	size, err := io.Copy(sum, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(sum.Sum(nil)), nil
}

// renameIfPresent moves from to to unless from is gone, which an earlier run
// that already moved it leaves.
func renameIfPresent(from, to string) error {
	if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("move %s: %s already exists", from, to)
	}
	return os.Rename(from, to)
}

// checkPrefix refuses unless the file at shorter is absent or begins the file
// at longer, so a move never drops turns either copy has.
func checkPrefix(shorter, longer string) error {
	existing, err := os.Open(shorter)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer existing.Close()
	arriving, err := os.Open(longer)
	if err != nil {
		return err
	}
	defer arriving.Close()
	left := make([]byte, 64<<10)
	right := make([]byte, 64<<10)
	for {
		n, readErr := io.ReadFull(existing, left)
		if n > 0 {
			if _, err := io.ReadFull(arriving, right[:n]); err != nil || string(left[:n]) != string(right[:n]) {
				return fmt.Errorf("the transcript here and the one arriving have diverged")
			}
		}
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
