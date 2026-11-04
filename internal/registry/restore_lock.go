package registry

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

const restoreWait = 2 * time.Minute

func restoreLockPath(path string) string {
	return path + ".restore-lock"
}

func holdRestoreLock(path string) (func(), error) {
	lockPath := restoreLockPath(path)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("lock the registry for a restore: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another restore of the registry is running")
	}
	return func() {
		_ = os.Remove(lockPath)
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

// waitForRestore keeps every opener, including a daemon a service manager respawns, away from a registry being swapped.
func waitForRestore(path string) error {
	disk, _, _ := strings.Cut(path, "?")
	lockPath := restoreLockPath(disk)
	deadline := time.Now().Add(restoreWait)
	for {
		file, err := os.Open(lockPath)
		if err != nil {
			return nil
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			file.Close()
			return nil
		}
		file.Close()
		if time.Now().After(deadline) {
			return fmt.Errorf("the registry is still being restored after %s", restoreWait)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
