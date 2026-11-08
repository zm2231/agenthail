package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
)

const queuedWithoutDaemon = "warning: daemon is not running; queued message will not be delivered until you start it (agenthail daemon start)"

var daemonRunning = func() bool {
	_, ok := daemon.IsRunning()
	return ok
}

func warnQueuedWithoutDaemon(w io.Writer) {
	if !daemonRunning() {
		fmt.Fprintln(w, queuedWithoutDaemon)
	}
}

// A coordinator send is refused by the daemon, so only the message crosses back.
func isTargetBusy(err error) bool {
	return errors.Is(err, delivery.ErrTargetBusy) || strings.Contains(err.Error(), delivery.ErrTargetBusy.Error())
}

func busyRefusal(target string) error {
	return fmt.Errorf("%w: %s is in a turn and --no-queue does not wait; run 'agenthail queue %s \"message\"' to deliver it when the turn ends, or 'agenthail steer %s \"message\"' to reach the turn now", delivery.ErrTargetBusy, target, target, target)
}
