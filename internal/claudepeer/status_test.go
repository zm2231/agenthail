package claudepeer

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestStatusNormalizesPeerValues(t *testing.T) {
	for _, test := range []struct {
		input surface.SessionStatus
		want  string
	}{
		{surface.StatusBusy, "busy"},
		{surface.StatusIdle, "idle"},
		{surface.StatusOffline, "offline"},
		{surface.StatusUnknown, "unknown"},
		{"paused", "unknown"},
	} {
		if got := status(surface.Session{Status: test.input}); got != test.want {
			t.Errorf("status(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
