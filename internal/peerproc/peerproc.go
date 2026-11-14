// Package peerproc reads, from the kernel, which process is on the other end
// of a Unix socket and which process started it.
package peerproc

import (
	"errors"
	"net"
)

type Peer struct {
	PID int
	UID int
}

var ErrUnsupported = errors.New("peer credentials are not available on this system")

func Of(conn net.Conn) (Peer, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, errors.New("not a Unix socket connection")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var peer Peer
	var readErr error
	if err := raw.Control(func(fd uintptr) { peer, readErr = peerOf(int(fd)) }); err != nil {
		return Peer{}, err
	}
	return peer, readErr
}
