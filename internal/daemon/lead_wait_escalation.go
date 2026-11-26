package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/prwatch"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

const defaultLeadWaitEscalate = 2 * time.Hour

func (d *Daemon) leadWaitEscalateAfter() time.Duration {
	config, err := dashconfig.Load()
	if err != nil || config.LeadWaitEscalateMinutes <= 0 {
		return defaultLeadWaitEscalate
	}
	return time.Duration(config.LeadWaitEscalateMinutes) * time.Minute
}

// A worker's wait on its lead moves to the person when the lead cannot act, or when the lead has not answered in time.
// A wait the worker said in its end of turn is a request, so it gets the ask window. A wait read from the turn gets the longer
// window, and only while the worker holds work. A worker whose pull request is still running its checks is not escalated by time.
func (d *Daemon) sweepLeadWaits(ctx context.Context, now time.Time) {
	ends, err := d.Registry.CurrentTurnEnds()
	if err != nil {
		d.log.Printf("lead waits: read turn ends: %s", err)
		return
	}
	judgments, err := d.Registry.CurrentTurnJudgments()
	if err != nil {
		d.log.Printf("lead waits: read turn judgments: %s", err)
		return
	}
	if !anyLeadWaitCandidate(ends, judgments) {
		return
	}
	rows, _, err := d.decisionRows(ctx)
	if err != nil {
		d.log.Printf("lead waits: read sessions: %s", err)
		return
	}
	leads, err := d.loadLeadDirectory()
	if err != nil {
		d.log.Printf("lead waits: read the leads: %s", err)
		return
	}
	byID := make(map[string]dashboardSession, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	explicitWindow, judgedWindow := d.askEscalateAfter(), d.leadWaitEscalateAfter()
	for _, row := range rows {
		state := row.TurnState
		if _, _, remote := hosts.Split(row.ID); remote || row.Archived || state == nil || !state.OnLead || state.TurnID == "" {
			continue
		}
		lead, ok := leads.leadOf(row.Project)
		if !ok {
			continue
		}
		explicit := state.Source == turnSourceEndTurn
		var since time.Time
		window := explicitWindow
		judgment, judged := judgments[row.ID]
		if explicit {
			if row.EndTurn == nil {
				continue
			}
			since = row.EndTurn.EndedAt
		} else {
			if !judged || judgment.TurnID != state.TurnID || len(row.HeldWork) == 0 {
				continue
			}
			since, window = judgment.EndedAt, judgedWindow
		}
		if hearerHolds(byID, d.publicSessionID(lead.sessionID)) && (now.Sub(since) < window || checksRunning(row.PRs)) {
			continue
		}
		d.escalateLeadWait(row.ID, state.TurnID, explicit, judgment, now)
	}
}

func anyLeadWaitCandidate(ends map[string]registry.TurnEnd, judgments map[string]registry.TurnJudgment) bool {
	for _, end := range ends {
		if end.WaitingOn != "" && end.CompletedTurnID != "" && end.LeadEscalated.IsZero() {
			return true
		}
	}
	for _, judgment := range judgments {
		if judgment.LeadEscalated.IsZero() && (judgment.State == turnWaitingPerson || len(judgment.Facts.AskedOf) > 0 || len(judgment.Facts.Awaiting) > 0) {
			return true
		}
	}
	return false
}

func checksRunning(prs []prwatch.View) bool {
	for _, pr := range prs {
		if pr.State == "open" && pr.Checks == "pending" {
			return true
		}
	}
	return false
}

func (d *Daemon) escalateLeadWait(sessionID, turnID string, explicit bool, judgment registry.TurnJudgment, now time.Time) {
	var moved bool
	var err error
	if explicit {
		moved, err = d.Registry.EscalateTurnEnd(sessionID, turnID, now)
	} else {
		moved, err = d.Registry.EscalateTurnJudgment(sessionID, turnID, now)
	}
	if err != nil {
		d.log.Printf("lead waits: move the wait of %s turn %s to the person: %s", d.resolveDisplay(sessionID), turnID, err)
		return
	}
	if !moved {
		return
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", sessionID, map[string]string{"source": "lead-wait-escalated"})
	if explicit {
		session, err := d.Registry.Session(sessionID)
		if err != nil {
			d.log.Printf("lead waits: read %s: %s", d.resolveDisplay(sessionID), err)
			return
		}
		d.pushWaitingTurn(session, turnID)
		return
	}
	judgment.LeadEscalated = now
	d.pushJudgedTurn(judgment)
}
