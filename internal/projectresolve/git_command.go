package projectresolve

import (
	"context"
	"os"
	"os/exec"
)

// No optional locks, so a call killed on its deadline cannot strand .git/index.lock.
func GitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "LC_ALL=C", "GIT_OPTIONAL_LOCKS=0")
	return command
}
