package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type revisionRow struct {
	Record        registry.CatalogPresentationRow
	Display       *SessionDisplay
	Current       bool
	CurrentReason string
	Attention     *SessionAttention
	LimitResume   *dashboardLimitResume
	Host          *dashboardHost
}

func newRevisionRow(record registry.CatalogPresentationRow, row dashboardSession) revisionRow {
	record.Freshness.ObservedAt = time.Time{}
	return revisionRow{Record: record, Display: row.Display, Current: row.Current, CurrentReason: row.CurrentReason, Attention: row.Attention, LimitResume: row.LimitResume, Host: row.Host}
}
