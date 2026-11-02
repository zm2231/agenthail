package cli

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/ledger"
)

type boardView struct {
	goals       []ledger.Goal
	asks        []ledger.Ask
	allAsks     map[string]ledger.Ask
	decisions   []ledger.Decision
	constraints []ledger.Clause
}

func ledgerIDNumber(id string) int {
	i := len(id)
	for i > 0 && id[i-1] >= '0' && id[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(id[i:])
	return n
}

func byLedgerID[T any](items []T, id func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return ledgerIDNumber(id(items[i])) < ledgerIDNumber(id(items[j])) })
}

func newBoardView(board *ledger.Board) boardView {
	v := boardView{goals: board.Goals(), asks: board.OpenAsks(), decisions: board.Decisions(), constraints: board.ProjectConstraints(), allAsks: map[string]ledger.Ask{}}
	for _, ask := range board.AllAsks() {
		v.allAsks[ask.ID] = ask
	}
	byLedgerID(v.goals, func(g ledger.Goal) string { return g.ID })
	byLedgerID(v.asks, func(a ledger.Ask) string { return a.ID })
	byLedgerID(v.decisions, func(d ledger.Decision) string { return d.ID })
	byLedgerID(v.constraints, func(c ledger.Clause) string { return c.ID })
	return v
}

func clauseLine(c *ledger.Clause) string {
	state := string(c.State)
	if c.SelfReported {
		state += ", self-reported"
	}
	owner := c.OwnerSessionID
	if owner == "" {
		owner = "unassigned"
	}
	line := fmt.Sprintf("%s: %s  [%s, %s] owner=%s", c.ID, c.Text, state, c.Source, owner)
	if c.ProposedBy != "" {
		line += " proposed by " + c.ProposedBy
	}
	if len(c.Sources) > 0 {
		line += " from " + strings.Join(c.Sources, ", ")
	}
	return line
}

func printClause(indent string, c *ledger.Clause) {
	fmt.Println(indent + clauseLine(c))
	for _, cut := range c.Cuts {
		status := "pending the person's ack"
		if cut.Acked {
			status = "acked"
		}
		fmt.Printf("%s  cut %s: %s (%s)\n", indent, cut.ID, cut.Reason, status)
	}
	for _, w := range c.Waits {
		resolved := ""
		if w.Resolved != "" {
			resolved = " -> " + w.Resolved
		}
		fmt.Printf("%s  wait: %s%s\n", indent, w.Condition, resolved)
	}
}

func (v boardView) print(attachment ledger.Attachment) {
	empty := len(v.goals) == 0 && len(v.asks) == 0 && len(v.decisions) == 0 && len(v.constraints) == 0
	for _, g := range v.goals {
		fmt.Printf("%s %s", g.ID, g.Name)
		if len(g.Asks) > 0 {
			fmt.Printf("  asks %s", strings.Join(g.Asks, ", "))
		}
		fmt.Println()
		for _, c := range g.Constraints {
			printClause("  constraint ", c)
		}
	}
	for _, ask := range v.asks {
		fmt.Printf("%s [%s] goal=%s from %s\n", ask.ID, ask.Type, ask.GoalID, ask.SourceUtterance)
		for _, c := range ask.Clauses {
			printClause("  ", c)
		}
	}
	for _, c := range v.constraints {
		printClause("project constraint ", &c)
	}
	for _, d := range v.decisions {
		on := d.GoalID
		if on == "" {
			on = "the project"
		}
		fmt.Printf("%s v%d on %s: %s [%s] from %s\n", d.ID, d.Version, on, d.Text, d.Principal, d.SourceUtterance)
	}
	printAttachment(attachment)
	if empty && len(attachment.Unattached) == 0 && len(attachment.Elsewhere) == 0 {
		fmt.Println("no goals, asks, decisions or captured words yet")
	}
}

func printAttachment(attachment ledger.Attachment) {
	if n := len(attachment.Unattached); n > 0 {
		fmt.Printf("unattached (%d):\n", n)
		for _, u := range attachment.Unattached {
			text := strings.Join(strings.Fields(u.Text), " ")
			if len(text) > 80 {
				text = text[:77] + "..."
			}
			fmt.Printf("  %s to %s: %s\n", u.ID, u.TargetID, text)
		}
	}
	refs := make([]string, 0, len(attachment.Elsewhere))
	for ref := range attachment.Elsewhere {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	for _, ref := range refs {
		fmt.Printf("%s attached elsewhere: %s\n", ref, attachment.Elsewhere[ref])
	}
}

func (a *App) ledgerGoals(store *ledger.Store, args []string) error {
	key, err := ledgerProjectKey(a)
	if err != nil {
		return err
	}
	board, err := store.Board(key)
	if err != nil {
		return err
	}
	goals := newBoardView(board).goalViews()
	if hasFlag(args, "--json") {
		return emit(goals)
	}
	if len(goals) == 0 {
		fmt.Println("no goals; create one with: agenthail ledger goal <name>")
		return nil
	}
	for _, g := range goals {
		fmt.Printf("%s %s\n", g.ID, g.Name)
		for _, c := range g.Clauses {
			printClause("  ", c)
		}
		for _, c := range g.Constraints {
			printClause("  constraint ", c)
		}
		for _, d := range g.Decisions {
			fmt.Printf("  decided %s: %s from %s\n", d.ID, d.Text, d.SourceUtterance)
		}
	}
	return nil
}

type goalView struct {
	ledger.Goal
	Clauses   []*ledger.Clause  `json:"clauses"`
	Decisions []ledger.Decision `json:"decisions,omitempty"`
}

func (v boardView) goalViews() []goalView {
	out := make([]goalView, 0, len(v.goals))
	for _, g := range v.goals {
		view := goalView{Goal: g, Clauses: []*ledger.Clause{}}
		for _, askID := range g.Asks {
			view.Clauses = append(view.Clauses, v.allAsks[askID].Clauses...)
		}
		for _, d := range v.decisions {
			if d.GoalID == g.ID {
				view.Decisions = append(view.Decisions, d)
			}
		}
		out = append(out, view)
	}
	return out
}
