package daemon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
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
		d.sweepLedgerQuestions(ctx, now)
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

func (d *Daemon) sweepLedgerQuestions(ctx context.Context, now time.Time) {
	store := d.ledgerStore()
	keys, err := store.ProjectsPlannedHere(ledger.EventQuestion)
	if err != nil {
		d.log.Printf("ledger questions: list projects: %s", err)
		return
	}
	for _, key := range keys {
		expiries, err := store.ExpireQuestions(key, now)
		if err != nil {
			d.log.Printf("ledger questions: expire in %s: %s", key, err)
			continue
		}
		for _, expiry := range expiries {
			d.announceExpiry(ctx, expiry)
		}
	}
}

func expiryNotice(e ledger.Expiry) string {
	q := e.Question
	switch e.Outcome {
	case ledger.ExpiryDefaulted:
		return fmt.Sprintf("[intent ledger] %s (%q) had no answer by its deadline, so its default applied as %s: %s. Tell the person in your next reply.", q.ID, q.Text, e.Effects[0], q.DefaultAnswer)
	case ledger.ExpiryCut:
		return fmt.Sprintf("[intent ledger] %s (%q) had no answer by its deadline. Its default (%s) narrows the work, so it did not apply: %s is a pending cut until the person acks it. Ask them again; keep the full scope meanwhile.", q.ID, q.Text, q.DefaultAnswer, e.Effects[0])
	}
	return fmt.Sprintf("[intent ledger] %s (%q) had no answer by its deadline. It is an outward act, so nothing was done and the person was notified. Do not act until they answer.", q.ID, q.Text)
}

func (d *Daemon) announceExpiry(ctx context.Context, e ledger.Expiry) {
	q := e.Question
	notice := expiryNotice(e)
	d.publishEvent("ledger.question."+string(e.Outcome), q.OwnerSessionID, e)
	if e.Outcome != ledger.ExpiryDefaulted {
		title, message, key := "A question needs your answer", fmt.Sprintf("%s: %s (recommended: %s)", q.ID, q.Text, q.Recommendation), "ledger:"+e.ProjectKey+":"+q.ID
		if err := d.notifyDesktop(title, message, q.OwnerSessionID, "agenthail-"+key, "", nil); err != nil {
			d.log.Printf("ledger questions: desktop notification for %s: %s", q.ID, err)
		}
		d.notifyPairedDevices(ctx, mobileNotification{Title: title, Message: message, SessionID: q.OwnerSessionID, EventType: "ledger.question.escalated", CollapseID: key})
	}
	session, err := d.Registry.Session(q.OwnerSessionID)
	if err != nil {
		d.log.Printf("ledger questions: %s owner %s: %s", q.ID, q.OwnerSessionID, err)
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		d.log.Printf("ledger questions: reach %s for %s: %s", session.ID, q.ID, err)
		return
	}
	key := fmt.Sprintf("ledger:%s:%s:%s", e.ProjectKey, q.ID, e.Outcome)
	if _, err := d.dispatcher().DeliverWithOptions(ctx, adapter, session, notice, key, surface.SendOptions{}); err != nil {
		d.log.Printf("ledger questions: tell %s about %s: %s", session.ID, q.ID, err)
	}
}
