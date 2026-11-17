package cli

import (
	"context"
	"encoding/json"
	"net/http"
)

// backlogLine claims the ready backlog items the coordinator session has not been told of. Any failure is empty, so it never stands in the session's way.
func (a *App) backlogLine(ctx context.Context, sessionID string) string {
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
	body, status, err := client.call(ctx, http.MethodPost, "/api/v1/ledger/backlog-line", nil, map[string]string{"sessionId": sessionID}, nil)
	if err != nil || status != http.StatusOK {
		return ""
	}
	var out struct {
		Line string `json:"line"`
	}
	if json.Unmarshal(body, &out) != nil {
		return ""
	}
	return out.Line
}
