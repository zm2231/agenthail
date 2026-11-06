//go:build !linux && !darwin

package listeners

import "errors"

func scan([]string) ([]Listener, error) {
	return nil, errors.New("listing listening ports is supported on Linux and macOS")
}
