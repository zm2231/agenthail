package surface

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
)

// StartFailure names a working folder that is gone. A command in its own
// process group skips os.StartProcess's check of its folder, so the child's
// failed chdir reports the program as missing instead.
func StartFailure(cmd *exec.Cmd, err error) error {
	var path *fs.PathError
	if cmd.Dir == "" || !errors.As(err, &path) {
		return err
	}
	if info, statErr := os.Stat(cmd.Dir); statErr == nil && info.IsDir() {
		return err
	}
	return DeliveryUnavailable(fmt.Errorf("the folder %s is gone", cmd.Dir))
}
