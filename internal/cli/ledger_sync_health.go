package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

type ledgerSyncHealth struct {
	ProjectKey string     `json:"projectKey"`
	Owner      string     `json:"owner"`
	OwnerName  string     `json:"ownerName"`
	OwnerState string     `json:"ownerState"`
	Cursor     int64      `json:"cursor"`
	OwnerHead  int64      `json:"ownerHead"`
	Lag        int64      `json:"lag"`
	Unreplayed int        `json:"unreplayed"`
	SyncedAt   *time.Time `json:"syncedAt,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}

func (h ledgerSyncHealth) problem() bool {
	return h.OwnerState != "connected" || h.LastError != "" || h.SyncedAt == nil
}

func (h ledgerSyncHealth) String() string {
	line := fmt.Sprintf("ledger sync %s: owner %s (%s)", h.ProjectKey, h.OwnerName, h.OwnerState)
	if h.SyncedAt != nil {
		line += fmt.Sprintf(", last synced %s ago, %d owner events behind", time.Since(*h.SyncedAt).Round(time.Second), h.Lag)
	} else {
		line += ", never synced since the daemon started"
	}
	line += fmt.Sprintf(", %d local events waiting to replay", h.Unreplayed)
	if h.LastError != "" {
		line += "; last error: " + h.LastError
	}
	return line
}

func (a *App) ledgerSyncHealth(ctx context.Context, r *registry.Registry) ([]ledgerSyncHealth, error) {
	owned, err := ledger.NewStore(r.WriteDB(), r.ReadDB()).RemotelyOwned()
	if err != nil || len(owned) == 0 {
		return nil, err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return nil, err
	}
	data, status, err := client.call(ctx, http.MethodGet, "/api/v1/ledger/sync/status", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, actionError(data)
	}
	var views []ledgerSyncHealth
	return views, json.Unmarshal(data, &views)
}
