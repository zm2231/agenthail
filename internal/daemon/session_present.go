package daemon

import (
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

type rowPresenter struct {
	now      time.Time
	leads    map[string]bool
	replaced map[string][]registry.CoordinatorTenure
}

func (d *Daemon) rowPresenter(now time.Time) (rowPresenter, error) {
	leads, err := d.Registry.LocalLeadSessions()
	if err != nil {
		return rowPresenter{}, err
	}
	replaced, err := d.Registry.ReplacedLeadTenures()
	if err != nil {
		return rowPresenter{}, err
	}
	return rowPresenter{now: now, leads: leads, replaced: replaced}, nil
}

func (p rowPresenter) present(row *dashboardSession, localID string) {
	if localID != "" {
		row.ReplacedLead = nil
		if tenures, replaced := p.replaced[localID]; replaced && !p.leads[localID] {
			row.ReplacedLead = tenures
		}
	}
	display := sessionDisplay(*row, p.now)
	row.Display = &display
	row.Attention = rowAttention(*row)
}

func (d *Daemon) presentedAttention(sessionID string, now time.Time) (*SessionAttention, error) {
	stored, found, err := d.Registry.CatalogProjection(sessionID)
	if err != nil || !found {
		return nil, err
	}
	var row dashboardSession
	if err := json.Unmarshal([]byte(stored), &row); err != nil {
		return nil, err
	}
	presenter, err := d.rowPresenter(now)
	if err != nil {
		return nil, err
	}
	presenter.present(&row, localSessionID(sessionID))
	return row.Attention, nil
}

func localSessionID(id string) string {
	if _, _, remote := hosts.Split(id); remote {
		return ""
	}
	return id
}
