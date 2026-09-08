package cli

import (
	"fmt"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

func parseTurnOptions(args []string) (surface.TurnOptions, error) {
	options := surface.TurnOptions{Effort: flagVal(args, "--effort"), Mode: flagVal(args, "--mode"), ServiceTier: flagVal(args, "--service-tier")}
	if path := flagVal(args, "--output-schema"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return options, fmt.Errorf("read output schema: %w", err)
		}
		options.OutputSchema = data
	}
	return options, nil
}
