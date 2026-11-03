package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

type mcpHistoryInput struct {
	View      string `json:"view,omitempty" jsonschema:"history (deliveries and their outcomes, the default) or queue (instructions waiting to be delivered)."`
	SessionID string `json:"sessionId,omitempty" jsonschema:"For history: only this session's entries."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Entries per page, 1 to 50; default 20."`
	Before    int64  `json:"before,omitempty" jsonschema:"An entry id from a previous page, to read further back."`
}

func (d *Daemon) mcpHistory(ctx context.Context, caller *mcpCaller, input mcpHistoryInput) (any, error) {
	if !caller.has("read.all") {
		return nil, refuse("capability_missing", "Delivery history and the queue span every session; reading them is for the voice operator.")
	}
	limit, err := pageLimit(input.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if input.Before > 0 {
		query.Set("before", strconv.FormatInt(input.Before, 10))
	}
	handler, path := d.dashboardHistoryHandler, "/api/v1/history"
	switch input.View {
	case "", "history":
		if input.SessionID != "" {
			query.Set("sessionId", input.SessionID)
		}
	case "queue":
		handler, path = d.mobileQueueHandler, "/api/v1/queue"
	default:
		return nil, refuse("invalid", "view is history or queue.")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	recorder := newActionRecorder()
	handler(recorder, request)
	result, refusal := mcpActionOutcome(recorder.reply())
	if refusal != nil {
		return nil, refusal
	}
	return json.RawMessage(result), nil
}
