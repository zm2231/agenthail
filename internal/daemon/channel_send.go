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

func (b daemonBroadcastDelivery) Coordinator(ctx context.Context, spec registry.CoordinatorSpec, message, source string) (*delivery.Receipt, string, error) {
	options := surface.SendOptions{SourceSessionID: source}
	if spec.HostID != "" {
		receipt, _, err := b.d.sendToPeerCoordinator(ctx, spec, message, options)
		return receipt, "", err
	}
	outcome, err := b.sender.Service.Deliver(ctx, spec, message, options)
	if err != nil {
		return nil, "", err
	}
	if outcome.Session == nil {
		return outcome.Receipt, "", nil
	}
	return outcome.Receipt, outcome.Session.ID, nil
}

func (d *Daemon) sendToPeerCoordinator(ctx context.Context, spec registry.CoordinatorSpec, message string, options surface.SendOptions) (*delivery.Receipt, bool, error) {
	host, ok := d.pairedHost(spec.HostID)
	if !ok {
		return nil, false, fmt.Errorf("host %s is not paired", spec.HostID)
	}
	if err := d.hostTransit(ctx, host); err != nil {
		return nil, false, err
	}
	request := map[string]any{"action": "coordinator-send", "name": spec.Name, "message": message}
	if options.Model != "" {
		request["model"] = options.Model
	}
	if options.Author != "" {
		request["author"] = string(options.Author)
	}
	if options.PersonName != "" {
		request["personName"] = options.PersonName
	}
	if options.SourceSessionID != "" {
		request["sourceSessionId"] = options.SourceSessionID
	}
	if _, err := d.routeSource(ctx, request, host); err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, false, err
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, nil)
	if err != nil {
		return nil, false, err
	}
	payload, err := hosts.ReadResponse(response)
	if err != nil {
		return nil, false, err
	}
	var answer struct {
		Result  delivery.Receipt `json:"result"`
		Started bool             `json:"started"`
	}
	if err := json.Unmarshal(payload, &answer); err != nil {
		return nil, false, err
	}
	return &answer.Result, answer.Started, nil
}
