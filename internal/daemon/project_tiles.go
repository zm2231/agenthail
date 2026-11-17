package daemon

import (
	"errors"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

const projectTilesPath = "/api/v1/projects/tiles"

type tileOrderRequest struct {
	Order    []string `json:"order"`
	Pinned   []string `json:"pinned"`
	Revision *int64   `json:"revision"`
}

func (d *Daemon) projectTilesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tiles, err := d.Registry.TileOrder()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"tileOrder": tiles})
	case http.MethodPut:
		var request tileOrderRequest
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.Revision == nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "revision is required: send the revision of the order this one replaces, 0 when there is none")
			return
		}
		tiles, err := d.Registry.SetTileOrder(request.Order, request.Pinned, *request.Revision)
		var stale *registry.StaleTileOrderError
		if errors.As(err, &stale) {
			writeAPIErrorWith(w, http.StatusConflict, "conflict", "The tile order changed since it was read; apply the change to this one and send it again.", map[string]any{"tileOrder": stale.Current})
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		d.publishEvent("state.changed", "", map[string]string{"source": "tiles"})
		writeDashboardJSON(w, http.StatusOK, map[string]any{"tileOrder": tiles})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT for the tile order.")
	}
}
