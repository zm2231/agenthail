package cli

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func localDisplayTime(at time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	seconds := strconv.FormatInt(at.Unix(), 10)
	args := []string{"-d", "@" + seconds, "+%x %X"}
	if runtime.GOOS == "darwin" {
		args = []string{"-r", seconds, "+%x %X"}
	}
	out, err := exec.CommandContext(ctx, "date", args...).Output()
	if err != nil {
		return "", fmt.Errorf("format display instant in the viewer locale: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
