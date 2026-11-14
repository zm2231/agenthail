//go:build !linux && !darwin

package peerproc

func peerOf(int) (Peer, error) { return Peer{}, ErrUnsupported }

func Parent(int) (int, error) { return 0, ErrUnsupported }

func OpenFiles(int) ([]string, error) { return nil, ErrUnsupported }
