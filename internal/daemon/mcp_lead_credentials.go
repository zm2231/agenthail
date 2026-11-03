package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type mcpCredentialReplacer interface {
	ReplaceMCPCredential(ctx context.Context, sessionID, url, token string) error
}

func (d *Daemon) reconcileCredentials() ([]string, error) {
	credentials := d.mcpCredentials.Load()
	if credentials == nil {
		return nil, nil
	}
	live, err := d.Registry.LiveCredentials()
	if err != nil {
		return nil, err
	}
	var undelivered []string
	var failed []error
	for id, held := range live {
		if !sameCapabilities(held.Capabilities, sessionCapabilities(held.Lead)) {
			if _, err := credentials.Rotate(id); err != nil {
				failed = append(failed, fmt.Errorf("rotate %s after its permissions changed: %w", d.resolveDisplay(id), err))
				continue
			}
			held.Delivered = false
		}
		if !held.Delivered {
			undelivered = append(undelivered, id)
		}
	}
	return undelivered, errors.Join(failed...)
}

func sameCapabilities(a, b []string) bool {
	return slices.Equal(slices.Compact(slices.Sorted(slices.Values(a))), slices.Compact(slices.Sorted(slices.Values(b))))
}

func (d *Daemon) deliverCredentials(sessionIDs []string) {
	credentials := d.mcpCredentials.Load()
	if credentials == nil {
		return
	}
	for _, id := range sessionIDs {
		err := credentials.Deliver(id, "", func(token string) error { return d.pushCredential(id, credentials.URL(), token) })
		if err != nil && !errors.Is(err, surfaces.ErrMCPDeliveryDeferred) {
			d.log.Printf("mcp: delivering %s's new credential is retried: %s", d.resolveDisplay(id), err)
		}
	}
}

func (d *Daemon) pushCredential(sessionID, url, token string) error {
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return nil
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return nil
	}
	replacer, ok := adapter.(mcpCredentialReplacer)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return replacer.ReplaceMCPCredential(ctx, sessionID, url, token)
}
