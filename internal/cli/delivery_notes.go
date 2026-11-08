package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
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

func isNothingToSteer(err error) bool {
	return errors.Is(err, surface.ErrNothingToSteer) || strings.Contains(err.Error(), surface.ErrNothingToSteer.Error())
}

func idleSteerRefusal(target string) error {
	return fmt.Errorf("%w; run 'agenthail send %s \"message\"' to start a turn", surface.ErrNothingToSteer, target)
}

func uncertainOutcome(receipt delivery.Receipt) bool {
	return receipt.Status == string(registry.DeliveryIntentSubmitted) || receipt.Evidence == surface.EvidenceSubmitted || receipt.Evidence == surface.EvidenceUnknown
}

func uncertainNote(target string) string {
	return fmt.Sprintf("It may have arrived; read 'agenthail last %s' before sending again.", target)
}
