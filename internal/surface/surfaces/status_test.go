package surfaces

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexStatusNormalizesUnknownValues(t *testing.T) {
	for _, input := range []any{"paused", map[string]any{"type": "paused"}, nil} {
		if got := codexStatus(input); got != surface.StatusUnknown {
			t.Errorf("codexStatus(%#v) = %q, want unknown", input, got)
		}
	}
}
