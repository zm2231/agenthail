package registry

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var effectiveUID = os.Geteuid

// sudo keeps HOME, and a directory, database or lock left root-owned there locks its person out.
func RefuseSudoHome() error {
	return refuseSudoHomeFor(defaultPath())
}

func refuseSudoHomeFor(path string) error {
	return refuseRootInAnothersHome(path, effectiveUID(), ownerOfPath)
}

func refuseRootInAnothersHome(path string, euid int, ownerOf func(string) (uint32, error)) error {
	if euid != 0 {
		return nil
	}
	disk, _, _ := strings.Cut(path, "?")
	for home := filepath.Dir(filepath.Dir(disk)); ; home = filepath.Dir(home) {
		owner, err := ownerOf(home)
		if err == nil {
			if owner == 0 {
				return nil
			}
			return fmt.Errorf("don't run agenthail with sudo; run it as %s", nameOfAccount(owner))
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read the owner of %s: %w", home, err)
		}
		if home == filepath.Dir(home) {
			return nil
		}
	}
}

func ownerOfPath(path string) (uint32, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("no owner reported for %s", path)
	}
	return stat.Uid, nil
}

func nameOfAccount(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if account, err := user.LookupId(id); err == nil {
		return account.Username
	}
	return "user " + id
}
