//go:build !linux

package claudehost

import (
	"errors"
	"syscall"
)

type processHandle struct{}

func openProcess(int) (processHandle, error) {
	return processHandle{}, errors.New("this system cannot hold a process handle, so a reused pid could be signalled instead")
}

func (processHandle) signal(syscall.Signal) error { return errors.ErrUnsupported }

func (processHandle) close() {}
