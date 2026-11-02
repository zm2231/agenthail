package cli

import (
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

func (a *App) ledgerWorkShow(store *ledger.Store, w ledgerWriteArgs, id string) error {
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	item, ok := board.WorkItem(id)
	if !ok {
		return fmt.Errorf("%q is not a work item or a work action; `agenthail ledger work` lists the items\n%s", id, ledgerWorkUsage)
	}
	if w.bools["--json"] {
		return emit(item)
	}
	fmt.Println(workLine(item))
	if len(item.Timeline) == 0 {
		return nil
	}
	fmt.Println("timeline:")
	for _, e := range item.Timeline {
		fmt.Println("  " + workEntryLine(e))
	}
	return nil
}

func workEntryLine(e ledger.WorkEntry) string {
	kind := e.Kind
	switch {
	case e.Kind == "state":
		kind = "-> " + string(e.State)
	case e.Kind == "log" && e.LogType != "":
		kind = "log (" + e.LogType + ")"
	}
	parts := []string{e.At.Local().Format("2006-01-02 15:04"), e.Actor, kind}
	if e.Text != "" {
		parts = append(parts, e.Text)
	}
	return strings.Join(parts, "  ")
}

func (a *App) ledgerWorkReady(store *ledger.Store, w ledgerWriteArgs, next bool) error {
	board, err := a.workBoard(store, w)
	if err != nil {
		return err
	}
	ready := board.Ready()
	if next && len(ready) > 1 {
		ready = ready[:1]
	}
	if w.bools["--json"] {
		if next {
			if len(ready) == 0 {
				return emit(nil)
			}
			return emit(ready[0])
		}
		return emit(ready)
	}
	if len(ready) == 0 {
		fmt.Println("no work item is ready: every open item waits on unfinished work, or none is open")
		return nil
	}
	for _, item := range ready {
		fmt.Println(workLine(item))
	}
	return nil
}
