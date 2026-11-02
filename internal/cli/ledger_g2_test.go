package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

func TestTheBoardShowsWhetherG2IsChecking(t *testing.T) {
	line := g2Line(daemon.G2Count{Since: time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local), Checked: 12, Gated: 3}, nil)
	if line != "G2: 12 turns checked since 2026-11-02 09:00:00, 3 gated" {
		t.Fatalf("line = %q", line)
	}
	if line := g2Line(daemon.G2Count{}, errors.New("the daemon is not running")); !strings.HasPrefix(line, "G2: no turn-end checks are running") {
		t.Fatalf("line without a daemon = %q", line)
	}
}
