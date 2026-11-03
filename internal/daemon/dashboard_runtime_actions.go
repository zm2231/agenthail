package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) doCodexLaunch(ctx context.Context, _ principal, _ dashboardActionRequest) (actionResult, error) {
	executable, executableErr := os.Executable()
	if executableErr != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", fmt.Sprintf("find Agenthail executable: %s", executableErr))
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	launch := d.launchCodex
	if launch == nil {
		launch = func(ctx context.Context, executable string) ([]byte, error) {
			return exec.CommandContext(ctx, executable, "launch", "codex").CombinedOutput()
		}
	}
	output, launchErr := launch(ctx, executable)
	if launchErr != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = launchErr.Error()
		} else {
			detail += ": " + launchErr.Error()
		}
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", detail)
	}
	if adapter := d.adapters().ForSurface(surface.KindCodex); adapter != nil {
		d.recordSurfaceHealth(ctx, adapter, nil)
	}
	return actionOK(map[string]any{"ok": true, "result": strings.TrimSpace(string(output))}), nil
}

func (d *Daemon) doRuntimeEnsure(ctx context.Context, _ principal, _ dashboardActionRequest) (actionResult, error) {
	adapter := d.adapters().ForSurface(surface.KindCodex)
	ensurer, ok := adapter.(surface.RuntimeEnsurer)
	if adapter == nil || !ok {
		return actionResult{}, conflict("Codex managed runtime cannot be repaired here")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if err := ensurer.EnsureRuntime(ctx); err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	d.recordSurfaceHealth(ctx, adapter, nil)
	return actionOK(map[string]any{"ok": true}), nil
}
