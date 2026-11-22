package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

type ledgerCheckpointReply struct {
	Instruction string            `json:"instruction"`
	Held        []ledger.WorkItem `json:"held"`
}

func (a *App) ledgerCheckpoint(ctx context.Context, sessionID string) string {
	return a.readLedgerCheckpoint(ctx, map[string]any{"sessionId": sessionID}).Instruction
}

func (a *App) ledgerStartContext(ctx context.Context, sessionID string, mcp bool) string {
	reply := a.readLedgerCheckpoint(ctx, map[string]any{"sessionId": sessionID, "start": true})
	var parts []string
	for _, part := range []string{ledger.HeldNote(reply.Held, mcp), reply.Instruction} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (a *App) readLedgerCheckpoint(ctx context.Context, request map[string]any) ledgerCheckpointReply {
	var client *daemonAPIClient
	var err error
	if a.daemonAPIOverride != nil {
		client, err = a.daemonAPIOverride()
	} else {
		client, err = newDaemonAPIClient()
	}
	if err != nil {
		return ledgerCheckpointReply{}
	}
	body, status, err := client.call(ctx, http.MethodPost, "/api/v1/ledger/checkpoint", nil, request, nil)
	if err != nil || status != http.StatusOK {
		return ledgerCheckpointReply{}
	}
	var out ledgerCheckpointReply
	if json.Unmarshal(body, &out) != nil {
		return ledgerCheckpointReply{}
	}
	return out
}
