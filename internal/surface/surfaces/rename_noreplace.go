package surfaces

import (
	"errors"
	"os"
	"syscall"
)

// renameError reports a no-replace rename the way os.Rename reports errors,
// so callers can test for os.ErrNotExist and os.ErrExist.
func renameError(from, to string, err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: errno}
	}
	return err
}
