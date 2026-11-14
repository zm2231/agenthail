//go:build !linux && !darwin

package peerproc

func peerOf(int) (Peer, error) { return Peer{}, ErrUnsupported }

func Parent(int) (int, error) { return 0, ErrUnsupported }
