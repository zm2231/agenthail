package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockName = "agenthail-worktrees.lock"

var lockPoll = 20 * time.Millisecond

// The CLI and the daemon are separate processes that both make and remove
// worktrees, so the lock is a file in the repository's common git directory,
// which every checkout of it shares.
func lockRepository(ctx context.Context, dir string) (func(), error) {
	common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, errors.Join(ErrNotRepository, err)
	}
	file, err := os.OpenFile(filepath.Join(common, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
