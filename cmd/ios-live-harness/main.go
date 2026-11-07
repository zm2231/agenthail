// Command ios-live-harness runs scratch Agenthail hosts for the iPhone live
// tests. It starts each host's daemon in the foreground under its own scratch
// HOME, serves a loopback control port the UI tests use to read pairing links
// and to stop and start hosts, runs the test command, and fails if any host
// process touched a network peer, socket or file outside what the harness set
// up for it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const (
	exitIsolation = 3
	exitSetup     = 4
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("ios-live-harness", flag.ContinueOnError)
	binary := flags.String("agenthail", "", "agenthail binary the hosts run")
	hosts := flags.Int("hosts", 2, "number of scratch hosts")
	control := flags.String("control", "127.0.0.1:0", "loopback address for the control port")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: ios-live-harness -agenthail <binary> [-hosts N] [-control 127.0.0.1:port] -- <test command> [args...]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	command := flags.Args()
	if *binary == "" || *hosts < 1 || len(command) == 0 {
		flags.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h, err := newHarness(*binary, *hosts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ios-live-harness: %v\n", err)
		return exitSetup
	}
	defer h.cleanup()
	if err := h.start(ctx, *control); err != nil {
		fmt.Fprintf(os.Stderr, "ios-live-harness: %v\n", err)
		h.shutdown()
		return h.exitCode(exitSetup)
	}

	status := h.runCommand(ctx, command)
	h.shutdown()
	return h.exitCode(status)
}

func (h *harness) runCommand(ctx context.Context, command []string) int {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), controlEnv+"="+h.controlURL, "TEST_RUNNER_"+controlEnv+"="+h.controlURL)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 30 * time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() > 0:
		return exit.ExitCode()
	default:
		fmt.Fprintf(os.Stderr, "ios-live-harness: test command: %v\n", err)
		return 1
	}
}

func (h *harness) exitCode(status int) int {
	fmt.Fprintf(os.Stderr, "ios-live-harness: %s\n", h.watch.summary())
	return verdict(os.Stderr, status, h.watch.violations(), h.watch.blind())
}

func verdict(out io.Writer, status int, violations, blind []string) int {
	if len(blind) > 0 {
		fmt.Fprintf(out, "ios-live-harness: the isolation check proves nothing:\n")
		for _, b := range blind {
			fmt.Fprintf(out, "  %s\n", b)
		}
	}
	if len(violations) > 0 {
		fmt.Fprintf(out, "ios-live-harness: %d isolation violations:\n", len(violations))
		for _, v := range violations {
			fmt.Fprintf(out, "  %s\n", v)
		}
		return exitIsolation
	}
	if status == 0 && len(blind) > 0 {
		return exitIsolation
	}
	return status
}
