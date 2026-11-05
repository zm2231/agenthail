package cli

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

type boardView struct {
	goals       []ledger.Goal
	asks        []ledger.Ask
	allAsks     map[string]ledger.Ask
	decisions   []ledger.Decision
	constraints []ledger.Clause
	questions   []ledger.Question
	targetCuts  []ledger.TargetCut
	waivers     []ledger.GateWaiver
	checks      map[string]ledger.GoalChecks
	retractions []ledger.Retraction
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
	v := boardView{goals: board.Goals(), asks: board.OpenAsks(), decisions: board.Decisions(), constraints: board.ProjectConstraints(),
		questions: sortedQuestions(board, true), targetCuts: board.TargetCuts(), waivers: board.G2Waivers(), allAsks: map[string]ledger.Ask{}, checks: map[string]ledger.GoalChecks{}, retractions: board.Retractions()}
	for _, c := range board.GoalChecks() {
		v.checks[c.GoalID] = c
	}
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
		fmt.Printf("%s  cut %s: %s (%s)\n", indent, cut.ID, cut.Reason, cutStatus(cut))
	}
	for _, item := range c.WorkItems {
		fmt.Printf("%s  work %s\n", indent, workLine(*item))
	}
	for _, w := range c.Waits {
		resolved := ""
		if w.Resolved != "" {
			resolved = " -> " + w.Resolved
		}
		fmt.Printf("%s  wait: %s%s\n", indent, w.Condition, resolved)
	}
}

func decisionLine(d ledger.Decision, on string) string {
	if d.ByPerson() {
		return fmt.Sprintf("%s v%d on %s: %s [%s] from %s", d.ID, d.Version, on, d.Text, d.Principal, d.SourceUtterance)
	}
	return fmt.Sprintf("%s v%d on %s: %s [applied by default from %s; not the person's decision, authorizes nothing outward]", d.ID, d.Version, on, d.Text, d.QuestionID)
}

func cutStatus(cut ledger.Cut) string {
	switch {
	case cut.Acked:
		return "acked"
	case cut.Withdrawn:
		return "withdrawn: " + cut.WithdrawnBecause()
	}
	return "pending the person's ack"
}

func (v boardView) print(attachment ledger.Attachment, shown int) {
	empty := len(v.goals) == 0 && len(v.asks) == 0 && len(v.decisions) == 0 && len(v.constraints) == 0 && len(v.questions) == 0
	for _, g := range v.goals {
		fmt.Printf("%s %s", g.ID, g.Name)
		if len(g.Asks) > 0 {
			fmt.Printf("  asks %s", strings.Join(g.Asks, ", "))
		}
		if g.DefaultCuts > 0 {
			fmt.Printf("  %d unanswered defaults became pending cuts", g.DefaultCuts)
		}
		if c, ok := v.checks[g.ID]; ok {
			fmt.Printf("  checks: %d runs, %d cuts raised, last %s", c.Runs, c.CutsRaised, c.LastCheckAt.Local().Format(time.DateTime))
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
		fmt.Println(decisionLine(d, on))
	}
	for _, t := range v.targetCuts {
		fmt.Printf("cut %s on %s: %s (%s)\n", t.ID, t.Target, t.Reason, cutStatus(t.Cut))
	}
	for _, q := range v.questions {
		fmt.Println(questionLine(q))
	}
	for _, waiver := range v.waivers {
		fmt.Printf("waiver: G2 stopped re-blocking %s for %s after %d tries\n", waiver.Item, waiver.SessionID, ledger.GateBlockCap)
	}
	for _, r := range v.retractions {
		fmt.Println(r)
	}
	printAttachment(attachment, shown)
	if empty && len(attachment.Unattached) == 0 && len(attachment.Elsewhere) == 0 {
		fmt.Println("no goals, asks, decisions or captured words yet")
	}
}

func printAttachment(attachment ledger.Attachment, shown int) {
	if n := len(attachment.Unattached); n > 0 {
		words := attachment.Unattached
		if shown > 0 && n > shown {
			words = words[n-shown:]
			fmt.Printf("captured words not attached (%d), newest %d; --all lists every one:\n", n, shown)
		} else {
			fmt.Printf("unattached (%d):\n", n)
		}
		for _, u := range words {
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
			fmt.Println("  " + decisionLine(d, g.ID))
		}
		for _, q := range g.Questions {
			fmt.Println("  " + questionLine(q))
		}
	}
	return nil
}

type goalView struct {
	ledger.Goal
	Clauses   []*ledger.Clause  `json:"clauses"`
	Decisions []ledger.Decision `json:"decisions,omitempty"`
	Questions []ledger.Question `json:"questions,omitempty"`
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
		for _, q := range v.questions {
			if q.GoalID == g.ID {
				view.Questions = append(view.Questions, q)
			}
		}
		out = append(out, view)
	}
	return out
}
