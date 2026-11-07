package cli

import (
	"fmt"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

func parseTurnOptions(args []string) (surface.TurnOptions, error) {
	options := surface.TurnOptions{ServiceTier: flagVal(args, "--service-tier")}
	if path := flagVal(args, "--output-schema"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return options, fmt.Errorf("read output schema: %w", err)
		}
		options.OutputSchema = data
	}
	return options, nil
}

func parseSendTurnOptions(args []string) (surface.TurnOptions, error) {
	if flagVal(args, "--effort") != "" || flagVal(args, "--mode") != "" {
		return surface.TurnOptions{}, surface.ErrTurnSettingsOnSend
	}
	return parseTurnOptions(args)
}

func parseLaunchSettings(args []string) surface.LaunchSettings {
	return surface.LaunchSettings{Effort: flagVal(args, "--effort"), Mode: flagVal(args, "--mode")}
}
