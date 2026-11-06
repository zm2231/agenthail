package daemon

import (
	"github.com/zm2231/agenthail/internal/registry"
)

type revisionRow struct {
	Record        registry.CatalogPresentationRow
	Display       *SessionDisplay
	Current       bool
	CurrentReason string
	Attention     *SessionAttention
	InboxSection  string
	LimitResume   *dashboardLimitResume
	Host          *dashboardHost
	ReplacedLead  []registry.CoordinatorTenure
}

func newRevisionRow(record registry.CatalogPresentationRow, row dashboardSession) revisionRow {
	return revisionRow{Record: record, Display: row.Display, Current: row.Current, CurrentReason: row.CurrentReason, Attention: row.Attention, InboxSection: row.InboxSection, LimitResume: row.LimitResume, Host: row.Host, ReplacedLead: row.ReplacedLead}
}
