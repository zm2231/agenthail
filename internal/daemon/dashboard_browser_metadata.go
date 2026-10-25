package daemon

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) dashboardBrowserMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, 405, "method_not_allowed", "Use GET")
		return
	}
	q := r.URL.Query()
	for key := range q {
		if key != "metadata" {
			writeAPIError(w, 400, "invalid_metadata", "Metadata cannot be combined with view parameters")
			return
		}
	}
	if q.Get("metadata") != "1" {
		writeAPIError(w, 400, "invalid_metadata", "metadata must be 1")
		return
	}
	state, err := d.readBrowserMetadata(r.Context())
	if err != nil {
		writeAPIError(w, 500, "internal_error", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, http.StatusOK, state)
}

func (d *Daemon) readBrowserMetadata(ctx context.Context) (dashboardState, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return dashboardState{}, err
	}
	surfaces, err := d.Registry.CatalogSurfaces()
	if err != nil {
		return dashboardState{}, err
	}
	state, err := d.dashboardMetadataState(config, surfaces)
	if err != nil {
		return state, err
	}
	rows := []dashboardSession{}
	_, err = d.Registry.ReadCatalogPresentation(ctx, func(read registry.CatalogPresentation) ([]string, error) {
		state.HostEpoch, state.CatalogSeq = read.HostEpoch, read.CatalogSeq
		for _, record := range read.Rows {
			var row dashboardSession
			if err := json.Unmarshal(record.Data, &row); err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return nil, nil
	})
	if err != nil {
		return state, err
	}
	state.Projects, err = d.presentationProjects(rows)
	return state, err
}

func (d *Daemon) presentationProjects(rows []dashboardSession) ([]dashboardProject, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, err
	}
	coordinators, err := d.Registry.Coordinators()
	if err != nil {
		return nil, err
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		return nil, err
	}
	return catalogProjects(rows, records, coordinators, view.archive.Projects, d.localHost(), d.coordinatorHostRef), nil
}
