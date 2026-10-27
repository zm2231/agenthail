package proxyacct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Deadlines around a proxy request must leave this much for reading the key.
const KeyReadLimit = 10 * time.Second

var keyCommandTimeout = KeyReadLimit

func readKeyCommand(ctx context.Context, argv []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, keyCommandTimeout)
	defer cancel()
	var stdout bytes.Buffer
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdout = &stdout
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	err := command.Run()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("it did not finish within %s", keyCommandTimeout)
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("it exited with status %d", exit.ExitCode())
		}
	}
	if err != nil {
		return "", withoutOutput(err, stdout.String())
	}
	return stdout.String(), nil
}

func withoutOutput(err error, output string) error {
	output = strings.TrimSpace(output)
	if output == "" || !strings.Contains(err.Error(), output) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), output, "[output]"))
}
