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
	ctx, ok := board.WorkContext(id)
	if !ok {
		return fmt.Errorf("%q is not a work item or a work action; `agenthail ledger work` lists the items\n%s", id, ledgerWorkUsage)
	}
	if w.bools["--json"] {
		return emit(ctx)
	}
	item := ctx.Item
	fmt.Println(workLine(item))
	if item.ImportedFrom != "" {
		fmt.Println("imported from " + item.ImportedFrom)
	}
	printWorkText("description", item.Description)
	printWorkText("acceptance", item.Acceptance)
	if c := ctx.Clause; c != nil {
		printClauseRef("for", c)
	} else {
		fmt.Println("found work: not in the person's intent until it is linked to a clause (agenthail ledger work link " + item.ID + " <clause>)")
	}
	if f := ctx.FoundIn; f != nil && f.Work != nil {
		fmt.Printf("found in %s [%s]: %s\n", f.Work.ID, f.Work.State, f.Work.Text)
	} else if f != nil && f.Clause != nil {
		printClauseRef("found in", f.Clause)
	}
	for _, d := range ctx.DependsOn {
		fmt.Printf("waits on %s [%s]: %s\n", d.ID, d.State, d.Text)
	}
	for _, d := range ctx.Dependents {
		fmt.Printf("blocks %s [%s]: %s\n", d.ID, d.State, d.Text)
	}
	if len(item.Timeline) == 0 {
		return nil
	}
	fmt.Println("timeline:")
	for _, e := range item.Timeline {
		fmt.Println("  " + workEntryLine(e))
	}
	return nil
}

func printWorkText(label, text string) {
	if text == "" {
		return
	}
	fmt.Printf("%s:\n", label)
	for _, line := range strings.Split(text, "\n") {
		fmt.Println("  " + line)
	}
}

func printClauseRef(label string, c *ledger.ClauseRef) {
	on := c.AskID
	if c.Goal != "" {
		on = fmt.Sprintf("%s in %s %s", c.AskID, c.GoalID, c.Goal)
	}
	fmt.Printf("%s %s [%s, %s] %s: %s\n", label, c.ID, c.State, c.Source, on, c.Text)
	for _, words := range c.Words {
		fmt.Printf("  the person, %s (%s): %s\n", words.At, words.ID, strings.Join(strings.Fields(words.Text), " "))
	}
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
