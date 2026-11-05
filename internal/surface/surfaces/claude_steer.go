package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

// Claude holds an envelope that arrives as a turn, so an agent's words reach
// the session only over its socket; the CLI writes a turn under its label.
func ownTurn(ctx context.Context) (person bool, err error) {
	switch surface.SourceSessionID(ctx) {
	case surface.PersonSessionID:
		return true, nil
	case "", surface.SystemSessionID, surface.CLISessionID:
		return false, nil
	}
	return false, surface.DeliveryTerminal(surface.ErrNotOwnTurn, surface.DeliveryInvalidRequest)
}
