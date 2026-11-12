package simhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockCreate takes the machine-wide lock scripts/ios-simulator.sh holds while it finds or creates a device.
func (h *Host) lockCreate(ctx context.Context) (func(), error) {
	file, err := os.OpenFile(filepath.Join(h.lockDir, "agenthail-simulator-create.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { file.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
