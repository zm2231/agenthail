package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

const ledgerStatusUsage = "usage: agenthail ledger status [<session|@alias>] [--json]   (or ledger current)"

func (a *App) ledgerStatus(store *ledger.Store, args []string) error {
	w, err := parseLedgerWrite(args)
	if err != nil {
		return err
	}
	if len(w.positional) > 2 {
		return fmt.Errorf("%s", ledgerStatusUsage)
	}
	selector := w.value("--from")
	if len(w.positional) == 2 {
		selector = w.positional[1]
	}
	session, err := a.sourceSessionID(context.Background(), selector)
	if err != nil {
		return err
	}
	if session == "" {
		return fmt.Errorf("ledger status needs to run inside a session, or to be given one\n%s", ledgerStatusUsage)
	}
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	status := board.SessionStatus(session)
	if w.bools["--json"] {
		return emit(status)
	}
	printStatus(status)
	return nil
}

func printStatus(s ledger.SessionStatus) {
	section := func(title string, rows []ledger.StatusRow, line func(ledger.StatusRow) string) {
		if len(rows) == 0 {
			return
		}
		fmt.Println(title + ":")
		for _, row := range rows {
			fmt.Println("  " + line(row))
		}
	}
	head := func(row ledger.StatusRow) string {
		return fmt.Sprintf("%s [%s] %s %s", row.ID, row.State, row.Priority, row.Text)
	}
	held := func(row ledger.StatusRow) string {
		line := head(row)
		if row.LastLog != nil {
			line += fmt.Sprintf("\n    last log (%s, %s): %s", row.LastLog.LogType, row.LastLog.At.Local().Format("2006-01-02 15:04"), row.LastLog.Text)
		} else {
			line += "\n    no log yet"
		}
		if h := row.Handoff; h != nil && len(h.Remaining) > 0 {
			line += "\n    handed over as remaining: " + strings.Join(h.Remaining, "; ")
		}
		return line
	}
	fmt.Println("status of " + s.SessionID)
	if len(s.Holding)+len(s.Rejected)+len(s.Waiting)+len(s.InReview) == 0 {
		fmt.Println("holds no work")
	}
	section("rejected, back to you", s.Rejected, func(row ledger.StatusRow) string {
		return held(row) + fmt.Sprintf("\n    %s sent it back: %s", row.RejectedBy, row.Rejection)
	})
	section("holding", s.Holding, held)
	section("waiting", s.Waiting, func(row ledger.StatusRow) string {
		line := held(row)
		if row.Reason != "" {
			line += "\n    blocked on: " + row.Reason
		}
		if len(row.WaitsOn) > 0 {
			line += "\n    waits on " + strings.Join(row.WaitsOn, ", ")
		}
		return line
	})
	section("submitted, waiting for review", s.InReview, head)
	section(fmt.Sprintf("others' work you could review (%d)", s.ReviewableTotal), s.Reviewable, func(row ledger.StatusRow) string {
		return head(row) + " assignee=" + row.Assignee
	})
	section("ready to take next", s.Ready, head)
}
