//go:build !darwin

package surfaces

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func processStartTime(pid int) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation(claudeProcessStartLayout, strings.TrimSpace(string(output)), time.UTC)
}
