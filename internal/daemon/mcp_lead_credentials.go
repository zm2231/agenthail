package daemon

import (
	"context"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type mcpCredentialReplacer interface {
	ReplaceMCPCredential(ctx context.Context, sessionID, url, token string) error
}

func (d *Daemon) reconcileLeadCredentials(previous, current map[string]bool) {
	for id := range previous {
		if !current[id] {
			d.reconcileSessionCredential(id)
		}
	}
	for id := range current {
		if !previous[id] {
			d.reconcileSessionCredential(id)
		}
	}
}

func (d *Daemon) reconcileSessionCredential(sessionID string) {
	credentials := d.mcpCredentials.Load()
	if credentials == nil {
		return
	}
	held, live, err := d.Registry.LiveCredentialCapabilities(sessionID)
	if err != nil || !live {
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		return
	}
	if slices.Equal(slices.Compact(slices.Sorted(slices.Values(held))), slices.Compact(slices.Sorted(slices.Values(d.sessionCapabilities(*session))))) {
		return
	}
	token, err := credentials.Rotate(sessionID)
	if err != nil {
		d.log.Printf("mcp: rotate %s after its permissions changed: %s", d.resolveDisplay(sessionID), err)
		return
	}
	d.deliverRotatedCredential(*session, credentials.URL(), token)
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
