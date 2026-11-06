package surfaces

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

// The ThreadStatus types of Codex's app-server protocol, from
// `codex app-server generate-json-schema` (codex-cli 0.163.0).
var codexProtocolThreadStatuses = []string{"notLoaded", "idle", "systemError", "active"}

func TestEveryCodexThreadStatusHasItsOwnMapping(t *testing.T) {
	for _, status := range codexProtocolThreadStatuses {
		if _, mapped := codexThreadStatuses[status]; !mapped {
			t.Errorf("Codex thread status %q has no mapping and would read as unknown", status)
		}
	}
	if len(codexThreadStatuses) != len(codexProtocolThreadStatuses) {
		t.Errorf("mapped %d Codex thread statuses, the protocol has %d", len(codexThreadStatuses), len(codexProtocolThreadStatuses))
	}
}

func TestCodexStatusReadsAnUnmappedValueAsUnknown(t *testing.T) {
	for _, input := range []any{"paused", map[string]any{"type": "paused"}, nil} {
		if got := codexStatus(input); got != surface.StatusUnknown {
			t.Errorf("codexStatus(%#v) = %q, want unknown", input, got)
		}
	}
}

func TestAThreadCodexHasNotLoadedKeepsThatStatusAndHasEnded(t *testing.T) {
	for status, want := range map[string]struct {
		status surface.SessionStatus
		ended  bool
	}{
		"notLoaded": {surface.StatusNotLoaded, true},
		"idle":      {surface.StatusIdle, false},
		"active":    {surface.StatusBusy, false},
	} {
		session := codexSession(map[string]any{"id": "thread", "status": map[string]any{"type": status}})
		if session.Status != want.status || session.Ended != want.ended {
			t.Errorf("%s: status=%q ended=%v, want %q ended=%v", status, session.Status, session.Ended, want.status, want.ended)
		}
	}
}
