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

func (d *Daemon) dashboardCodexLaunch(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	executable, executableErr := os.Executable()
	if executableErr != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("find Agenthail executable: %s", executableErr))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	output, launchErr := exec.CommandContext(ctx, executable, "launch", "codex").CombinedOutput()
	if launchErr != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = launchErr.Error()
		} else {
			detail += ": " + launchErr.Error()
		}
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", detail)
		return
	}
	if adapter := d.adapters().ForSurface(surface.KindCodex); adapter != nil {
		d.recordSurfaceHealth(ctx, adapter, nil)
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": strings.TrimSpace(string(output))})
	return
}

func (d *Daemon) dashboardRuntimeEnsure(w http.ResponseWriter, r *http.Request, request dashboardActionRequest) {
	adapter := d.adapters().ForSurface(surface.KindCodex)
	ensurer, ok := adapter.(surface.RuntimeEnsurer)
	if adapter == nil || !ok {
		writeAPIError(w, http.StatusConflict, "conflict", "Codex managed runtime cannot be repaired here")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := ensurer.EnsureRuntime(ctx); err != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	d.recordSurfaceHealth(ctx, adapter, nil)
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	return
}
