package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardSessionOperation(w http.ResponseWriter, r *http.Request, adapter surface.Surface, session *surface.Session, action string, forkOptions surface.ForkOptions, queue surface.NativeQueueRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	var err error
	switch action {
	case "session-fork":
		forker, ok := adapter.(surface.SessionForker)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		if forkOptions.Cwd != "" {
			forkOptions.Cwd, err = dashboardStartCwd(forkOptions.Cwd)
			if err != nil {
				break
			}
		}
		var fork *surface.Session
		fork, err = forker.ForkSession(ctx, session, forkOptions)
		if err == nil && fork == nil {
			err = fmt.Errorf("fork returned no session")
		}
		if err == nil {
			err = d.Registry.RegisterSession(*fork)
			result = map[string]any{"session": fork}
		}
	case "native-queue":
		native, ok := adapter.(surface.NativeQueuer)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		result, err = native.NativeQueue(ctx, session, queue)
	default:
		lifecycle, ok := adapter.(surface.SessionLifecycle)
		if !ok {
			err = surface.ErrUnsupported
			break
		}
		result, err = lifecycle.SessionAction(ctx, session, strings.TrimPrefix(action, "session-lifecycle-"))
	}
	if err != nil {
		writeDashboardJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "unknown": surface.IsDeliveryOutcomeUnknown(err), "error": err.Error()})
		return
	}
	data, _ := json.Marshal(result)
	if action != "session-lifecycle-status" && action != "session-lifecycle-logs" && !(action == "native-queue" && queue.Action == "list") {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: action + ": " + string(data)})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}
