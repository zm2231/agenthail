package daemon

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const codexUsageTimeout = 5 * time.Second

type codexUsageReader interface {
	Usage(ctx context.Context) (*surface.Usage, error)
}

func (d *Daemon) codexUsageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	reader, ok := d.adapters().ForSurface(surface.KindCodex).(codexUsageReader)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "codex_unavailable", "Codex is not available on this host.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), codexUsageTimeout)
	defer cancel()
	usage, err := reader.Usage(ctx)
	switch {
	case errors.Is(err, surfaces.ErrNoCodexUsage):
		writeDashboardJSON(w, http.StatusOK, map[string]any{"usage": nil, "usageProblem": "No Codex usage recorded yet"})
	case err != nil:
		writeAPIError(w, http.StatusServiceUnavailable, "codex_usage_unavailable", err.Error())
	default:
		writeDashboardJSON(w, http.StatusOK, map[string]any{"usage": usage, "usageProblem": ""})
	}
}
