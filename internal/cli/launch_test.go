package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type scopedCodex struct {
	surface.Surface
	scope error
}

func (s scopedCodex) DesktopScope() error { return s.scope }

func TestLaunchCodexStopsBeforeOpeningDesktopOutsideItsScope(t *testing.T) {
	refused := errors.New("Codex Desktop is not used from this Codex home (/scratch/.codex); set AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT to reach Codex Desktop from this Codex home")
	if err := launchCodex(scopedCodex{scope: refused}); !errors.Is(err, refused) {
		t.Fatalf("launch from a Codex home of its own returned %v, want the opt-in before Desktop is looked for", err)
	}
}

func TestLaunchCodexFromAScratchHomeNamesTheOptIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	err := launchCodex(surfaces.NewCodex(""))
	if err == nil || !strings.Contains(err.Error(), "AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT") {
		t.Fatalf("launch from a scratch home: %v", err)
	}
}
