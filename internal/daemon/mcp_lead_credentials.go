package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

type mcpCredentialReplacer interface {
	ReplaceMCPCredential(ctx context.Context, sessionID, url, token string) error
}

func (d *Daemon) reconcileCredentials() ([]func(), error) {
	credentials := d.mcpCredentials.Load()
	if credentials == nil {
		return nil, nil
	}
	live, err := d.Registry.LiveCredentials()
	if err != nil {
		return nil, err
	}
	leads, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return nil, err
	}
	var deliveries []func()
	var failed []error
	for id, held := range live {
		if sameCapabilities(held, sessionCapabilities(leads[id])) {
			continue
		}
		token, err := credentials.Rotate(id)
		if err != nil {
			failed = append(failed, fmt.Errorf("rotate %s after its permissions changed: %w", d.resolveDisplay(id), err))
			continue
		}
		url := credentials.URL()
		deliveries = append(deliveries, func() { d.deliverRotatedCredential(id, url, token) })
	}
	return deliveries, errors.Join(failed...)
}

func sameCapabilities(a, b []string) bool {
	return slices.Equal(slices.Compact(slices.Sorted(slices.Values(a))), slices.Compact(slices.Sorted(slices.Values(b))))
}

func (d *Daemon) deliverRotatedCredential(sessionID, url, token string) {
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return
	}
	replacer, ok := adapter.(mcpCredentialReplacer)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := replacer.ReplaceMCPCredential(ctx, sessionID, url, token); err != nil {
		d.log.Printf("mcp: %s gets its new credential when it next opens: %s", d.resolveDisplay(sessionID), err)
	}
}
