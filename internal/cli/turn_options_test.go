package cli

import (
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestASendRefusesEffortAndModeButKeepsPerTurnOptions(t *testing.T) {
	for _, args := range [][]string{{"--effort", "high"}, {"--mode", "plan"}} {
		if _, err := parseSendTurnOptions(args); !errors.Is(err, surface.ErrTurnSettingsOnSend) {
			t.Fatalf("%v: err=%v", args, err)
		}
	}
	options, err := parseSendTurnOptions([]string{"--service-tier", "flex"})
	if err != nil || options.ServiceTier != "flex" {
		t.Fatalf("options=%+v err=%v", options, err)
	}
	if got := parseLaunchSettings([]string{"--effort", "high", "--mode", "plan"}); got != (surface.LaunchSettings{Effort: "high", Mode: "plan"}) {
		t.Fatalf("launch=%+v", got)
	}
}
