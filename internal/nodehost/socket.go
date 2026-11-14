package nodehost

import (
	"fmt"
	"runtime"
)

// sun_path holds the socket path and its terminating NUL.
var sunPathBytes = map[string]int{"darwin": 104, "freebsd": 104, "openbsd": 104, "netbsd": 104, "linux": 108}

func CheckSocketPath(path string) error {
	return checkSocketPath(path, runtime.GOOS)
}

func checkSocketPath(path, goos string) error {
	limit, ok := sunPathBytes[goos]
	if !ok {
		limit = 104
	}
	if len(path) >= limit {
		return fmt.Errorf("its socket path %s is %d bytes, and Unix sockets on %s allow at most %d; use a shorter home directory", path, len(path), goos, limit-1)
	}
	return nil
}
