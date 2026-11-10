package daemon

import (
	"context"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerQuestionSweep = 30 * time.Second

func (d *Daemon) apiLedgerQuestionHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.QuestionRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, r, req.SessionID) || !d.knownLedgerProject(w, r, req.ProjectKey, req.SessionID) {
		return
	}
	q, err := d.ledgerStore().AskQuestion(req, time.Now())
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, q)
}

func (d *Daemon) runLedgerSweeps(ctx context.Context) {
	ticker := time.NewTicker(ledgerQuestionSweep)
	defer ticker.Stop()
	sweep := func(now time.Time) {
		d.sweepLedgerQuestions(now)
		d.syncAllLedgerAsks()
		d.sweepLedgerChecks(ctx, now)
	}
	sweep(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			sweep(now)
		}
	}
}

func (d *Daemon) sweepLedgerQuestions(now time.Time) {
	store := d.ledgerStore()
	keys, err := store.ProjectsPlannedHere(ledger.EventQuestion)
	if err != nil {
		d.log.Printf("ledger questions: list projects: %s", err)
		return
	}
	for _, key := range keys {
		if _, err := store.ExpireQuestions(key, now); err != nil {
			d.log.Printf("ledger questions: expire in %s: %s", key, err)
		}
	}
}
