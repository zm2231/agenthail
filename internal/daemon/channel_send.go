package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type daemonBroadcastDelivery struct {
	d      *Daemon
	sender coordinator.Sender
}

func (b daemonBroadcastDelivery) Session(ctx context.Context, sessionID, message, source string) (*delivery.Receipt, error) {
	session, err := b.d.Registry.Session(sessionID)
	if err != nil {
		return nil, err
	}
	adapter, err := b.d.adapters().For(session)
	if err != nil {
		return nil, err
	}
	if err := b.d.ensureDashboardWritable(ctx, adapter, session); err != nil {
		return nil, err
	}
	return b.sender.DeliverWithOptions(ctx, adapter, session, message, "", surface.SendOptions{SourceSessionID: source})
}

func (b daemonBroadcastDelivery) PeerCoordinator(ctx context.Context, spec registry.CoordinatorSpec, message, source string) (*delivery.Receipt, error) {
	host, ok := b.d.pairedHost(spec.HostID)
	if !ok {
		return nil, fmt.Errorf("host %s is not paired", spec.HostID)
	}
	request := map[string]any{"action": "coordinator-send", "name": spec.Name, "message": message}
	if source != "" {
		if owner, err := b.d.Registry.SessionHost(source); err == nil && owner == "" {
			request["sourceSessionId"] = hosts.Qualify(b.d.hostIdentity().ID, source)
			request["sourceName"] = b.d.senderName(source)
		}
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, nil)
	if err != nil {
		return nil, err
	}
	payload, err := hosts.ReadResponse(response)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Result delivery.Receipt `json:"result"`
	}
	if err := json.Unmarshal(payload, &answer); err != nil {
		return nil, err
	}
	return &answer.Result, nil
}
