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
	Log        string     `json:"log"`
	LogThrough int64      `json:"logThrough,omitempty"`
	FirstDiff  int64      `json:"firstDifference,omitempty"`
	LogError   string     `json:"logError,omitempty"`
}

func (h ledgerSyncHealth) problem() bool {
	return h.OwnerState != "connected" || h.LastError != "" || h.SyncedAt == nil || h.Log == "mismatch" || h.LogError != ""
}

func (h ledgerSyncHealth) logLine() string {
	switch {
	case h.LogError != "":
		return "; log check failed: " + h.LogError
	case h.Log == "match":
		return fmt.Sprintf("; log matches the owner's through owner position %d", h.LogThrough)
	case h.Log == "mismatch":
		return fmt.Sprintf("; LOG DIFFERS from the owner's: first difference at owner position %d (checked through %d)", h.FirstDiff, h.LogThrough)
	}
	return "; log not checked yet"
}

func (h ledgerSyncHealth) String() string {
	line := fmt.Sprintf("ledger sync %s: owner %s (%s)", h.ProjectKey, h.OwnerName, h.OwnerState)
	if h.SyncedAt != nil {
		line += fmt.Sprintf(", last synced %s ago, %d owner events behind", time.Since(*h.SyncedAt).Round(time.Second), h.Lag)
	} else {
		line += ", never synced since the daemon started"
	}
	line += fmt.Sprintf(", %d local events waiting to replay", h.Unreplayed) + h.logLine()
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
