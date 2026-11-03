package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type mcpCredentialReplacer interface {
	ReplaceMCPCredential(ctx context.Context, sessionID, url, token string) error
}

func (d *Daemon) coordinatorStarted(sessionID string) {
	d.leads.unsettle(sessionID)
	d.syncLeads()
}

func (d *Daemon) reconcileLeadCredentials(previous, current map[string]bool) ([]func(), error) {
	var deliveries []func()
	var failed []error
	reconcile := func(id string) {
		deliver, err := d.reconcileSessionCredential(id)
		failed = append(failed, err)
		deliveries = appendDelivery(deliveries, deliver)
	}
	for id := range previous {
		if !current[id] {
			reconcile(id)
		}
	}
	for id := range current {
		if !previous[id] {
			reconcile(id)
		}
	}
	return deliveries, errors.Join(failed...)
}

func appendDelivery(deliveries []func(), deliver func()) []func() {
	if deliver == nil {
		return deliveries
	}
	return append(deliveries, deliver)
}

// The delivery can block on a runtime, so callers run it after releasing the lead lock.
func (d *Daemon) reconcileSessionCredential(sessionID string) (func(), error) {
	credentials := d.mcpCredentials.Load()
	if credentials == nil {
		return nil, nil
	}
	held, live, err := d.Registry.LiveCredentialCapabilities(sessionID)
	if err != nil || !live {
		return nil, err
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", sessionID, err)
	}
	if slices.Equal(slices.Compact(slices.Sorted(slices.Values(held))), slices.Compact(slices.Sorted(slices.Values(d.sessionCapabilities(*session))))) {
		return nil, nil
	}
	token, err := credentials.Rotate(sessionID)
	if err != nil {
		return nil, fmt.Errorf("rotate %s after its permissions changed: %w", d.resolveDisplay(sessionID), err)
	}
	url, rotated := credentials.URL(), *session
	return func() { d.deliverRotatedCredential(rotated, url, token) }, nil
}

func (d *Daemon) deliverRotatedCredential(session surface.Session, url, token string) {
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return
	}
	replacer, ok := adapter.(mcpCredentialReplacer)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := replacer.ReplaceMCPCredential(ctx, session.ID, url, token); err != nil {
		d.log.Printf("mcp: %s gets its new credential when it next opens: %s", d.resolveDisplay(session.ID), err)
	}
}
