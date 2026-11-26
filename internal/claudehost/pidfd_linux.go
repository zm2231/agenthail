package claudehost

import (
	"syscall"

	"golang.org/x/sys/unix"
)

type processHandle struct{ fd int }

func openProcess(pid int) (processHandle, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	return processHandle{fd: fd}, err
}

func (h processHandle) signal(signal syscall.Signal) error {
	return unix.PidfdSendSignal(h.fd, signal, nil, 0)
}

func (h processHandle) close() { _ = unix.Close(h.fd) }
