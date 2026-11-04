package registry

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
)

var (
	restoreWait     = 2 * time.Minute
	restoreHoldWait = 10 * time.Second
)

func registryLockPath(path string) string {
	return path + ".lock"
}

// Every open holds this shared until Close, and a restore needs it exclusively, so no process can be left writing to the file a restore sets aside.
func holdRestoreLock(path string) (func(), error) {
	lockPath := registryLockPath(path)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("lock the registry for a restore: %w", err)
	}
	deadline := time.Now().Add(restoreHoldWait)
	for syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("the registry is open in another process, such as a running agenthail command; stop it and run this again (lsof %s lists them)", shellQuote(lockPath))
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() { _ = file.Close() }, nil
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// os.OpenFile sets close-on-exec, so a process this one spawns does not keep the lock.
// A registry whose directory cannot hold the lock file opens without it: nothing can restore over a registry there either.
func holdRegistryOpen(path string) (*os.File, error) {
	disk, _, _ := strings.Cut(path, "?")
	file, err := os.OpenFile(registryLockPath(disk), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, nil
	}
	deadline := time.Now().Add(restoreWait)
	for syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("the registry is still being restored after %s", restoreWait)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return file, nil
}
