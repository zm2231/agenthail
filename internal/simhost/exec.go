package simhost

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Command struct {
	Name  string
	Args  []string
	Env   []string
	Stdin []byte
}

type Exec func(ctx context.Context, cmd Command) ([]byte, error)

type CommandError struct {
	Command string
	Err     error
	Stderr  string
}

func (e *CommandError) Error() string {
	if e.Stderr == "" {
		return fmt.Sprintf("%s: %v", e.Command, e.Err)
	}
	return fmt.Sprintf("%s: %v: %s", e.Command, e.Err, e.Stderr)
}

func (e *CommandError) Unwrap() error { return e.Err }

const commandWaitDelay = 2 * time.Second

func RunCommand(ctx context.Context, cmd Command) ([]byte, error) {
	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.WaitDelay = commandWaitDelay
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	if cmd.Stdin != nil {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		text := strings.TrimSpace(stderr.String())
		if len(text) > 2048 {
			text = text[:2048] + "…"
		}
		return out, &CommandError{Command: cmd.Name + " " + firstArgs(cmd.Args), Err: err, Stderr: text}
	}
	return out, nil
}

func firstArgs(args []string) string {
	if len(args) > 2 {
		args = args[:2]
	}
	return strings.Join(args, " ")
}
