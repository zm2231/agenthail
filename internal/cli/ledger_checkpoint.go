package cli

import (
	"context"
	"encoding/json"
	"net/http"
)

// ledgerCheckpoint asks the local daemon how many of the session's words are provisional. A checkpoint never stands in the way of the command that reached it, so any failure is empty.
func (a *App) ledgerCheckpoint(ctx context.Context, sessionID string) string {
	var client *daemonAPIClient
	var err error
	if a.daemonAPIOverride != nil {
		client, err = a.daemonAPIOverride()
	} else {
		client, err = newDaemonAPIClient()
	}
	if err != nil {
		return ""
	}
	body, status, err := client.call(ctx, http.MethodPost, "/api/v1/ledger/checkpoint", nil, map[string]string{"sessionId": sessionID}, nil)
	if err != nil || status != http.StatusOK {
		return ""
	}
	var out struct {
		Instruction string `json:"instruction"`
	}
	if json.Unmarshal(body, &out) != nil {
		return ""
	}
	return out.Instruction
}
