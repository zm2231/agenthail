package daemon

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

type checkRequest struct {
	ProjectKey string `json:"projectKey"`
	SessionID  string `json:"sessionId"`
	GoalID     string `json:"goalId"`
}

func (d *Daemon) apiLedgerCheckHandler(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, req.SessionID) || !d.knownLedgerProject(w, req.ProjectKey) {
		return
	}
	board, err := d.ledgerStore().Board(req.ProjectKey)
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	if !slices.ContainsFunc(board.Goals(), func(g ledger.Goal) bool { return g.ID == req.GoalID }) {
		writeLedgerError(w, &ledger.Refusal{Reason: req.GoalID + " is not a goal in " + req.ProjectKey})
		return
	}
	d.checks.mu.Lock()
	queued := d.checks.enqueue(checkTarget{project: req.ProjectKey, goal: req.GoalID}, true)
	d.checks.mu.Unlock()
	go d.sweepLedgerChecks(context.Background(), time.Now())
	writeDashboardJSON(w, http.StatusOK, map[string]any{"projectKey": req.ProjectKey, "goalId": req.GoalID, "queued": queued})
}

func (d *Daemon) apiLedgerCheckReportHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.CheckReport
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	now := time.Now()
	d.checks.mu.Lock()
	running := d.checks.running
	if running == nil || running.run != req.Run {
		d.checks.mu.Unlock()
		writeLedgerError(w, &ledger.Refusal{Reason: req.Run + " is not the check running on this host"})
		return
	}
	req.ProjectKey = running.project
	outcome, err := d.ledgerStore().ReportCheck(req, now)
	if err != nil {
		d.checks.mu.Unlock()
		writeLedgerError(w, err)
		return
	}
	d.checks.running = nil
	d.checks.mu.Unlock()
	d.publishEvent("ledger.check."+outcome.Result, req.Run, outcome)
	writeDashboardJSON(w, http.StatusOK, outcome)
	go func() {
		ctx := context.Background()
		d.checks.mu.Lock()
		defer d.checks.mu.Unlock()
		d.endChecker(ctx, req.Run, time.Now())
		d.startNextCheck(ctx, d.ledgerStore(), time.Now())
	}()
}
