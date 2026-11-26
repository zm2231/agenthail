package daemon

import (
	"fmt"
	"slices"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	turnDone          = "done"
	turnWaitingPerson = "waiting_person"
	turnWaitingWork   = "waiting_work"
	turnContinuing    = "continuing"

	turnSourceFacts   = "facts"
	turnSourceJudge   = "judge"
	turnSourceEndTurn = "endTurn"
)

type dashboardTurnState struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source"`
	// TurnID and Pushed name a judged needs-you turn and whether its host pushed it to the person's devices, so a viewer pushes the same turn once under the host's key.
	TurnID   string `json:"turnId,omitempty"`
	Pushed   bool   `json:"pushed,omitempty"`
	Headline string `json:"headline,omitempty"`
}

type leadRef struct {
	sessionID, name string
}

type leadDirectory struct {
	byProject map[string]leadRef
	aliases   map[string]string
}

func (d *Daemon) loadLeadDirectory() (leadDirectory, error) {
	specs, err := d.Registry.Coordinators()
	if err != nil {
		return leadDirectory{}, err
	}
	aliases, err := d.Registry.ListAliases()
	if err != nil {
		return leadDirectory{}, err
	}
	directory := leadDirectory{byProject: make(map[string]leadRef, len(specs)), aliases: make(map[string]string, len(aliases))}
	for _, spec := range specs {
		if spec.SessionID != "" {
			directory.byProject[spec.ProjectID] = leadRef{sessionID: spec.SessionID, name: spec.Name}
		}
	}
	for _, alias := range aliases {
		directory.aliases[alias.SessionID] = alias.Name
	}
	return directory, nil
}

func (l leadDirectory) leadOf(project *projectresolve.CatalogProject) (leadRef, bool) {
	if project == nil {
		return leadRef{}, false
	}
	if lead, ok := l.byProject[project.ID]; ok {
		return lead, true
	}
	lead, ok := l.byProject[project.LocalID]
	return lead, ok
}

func (l leadDirectory) named(sessionID string) string {
	if alias := l.aliases[sessionID]; alias != "" {
		return "@" + alias
	}
	return "a session"
}

// The facts recorded when the turn ended come before the judge, and a worker with a lead waits on its lead rather than the person, since the lead is who it talks to.
func turnStateOf(row *dashboardSession, judgment *registry.TurnJudgment, leads leadDirectory) *dashboardTurnState {
	if row.Ask.asksPerson() {
		return &dashboardTurnState{State: turnWaitingPerson, Source: turnSourceFacts}
	}
	lead, hasLead := leads.leadOf(row.Project)
	isLead := hasLead && lead.sessionID == row.ID
	if judgment != nil && judgment.TurnID != "" && row.EndTurn.waits() && judgment.TurnID != row.EndTurn.TurnID {
		judgment = nil
	}
	if judgment != nil {
		facts := judgment.Facts
		var awaited []string
		for _, target := range append(slices.Clone(facts.AskedOf), facts.Awaiting...) {
			if target != registry.AskToPerson && !slices.Contains(awaited, target) {
				awaited = append(awaited, target)
			}
		}
		switch {
		case hasLead && !isLead && slices.Contains(awaited, lead.sessionID):
			return &dashboardTurnState{State: turnWaitingWork, Reason: "@" + lead.name, Source: turnSourceFacts}
		case len(awaited) > 0:
			return &dashboardTurnState{State: turnWaitingWork, Reason: namedSessions(awaited, leads), Source: turnSourceFacts}
		case len(facts.Children) > 0:
			return &dashboardTurnState{State: turnWaitingWork, Reason: namedSessions(facts.Children, leads), Source: turnSourceFacts}
		case facts.BackgroundRunning:
			return &dashboardTurnState{State: turnWaitingWork, Reason: "background work", Source: turnSourceFacts}
		}
	}
	if end := row.EndTurn; end.waits() {
		if hasLead && !isLead {
			return &dashboardTurnState{State: turnWaitingWork, Reason: "@" + lead.name, Source: turnSourceEndTurn}
		}
		return &dashboardTurnState{State: turnWaitingPerson, Reason: end.WaitingOn, Source: turnSourceEndTurn, TurnID: end.TurnID, Pushed: end.WaitPushed, Headline: shownLine(end.WaitingOn, headlineShownRunes)}
	}
	if judgment != nil {
		switch judgment.State {
		case turnWaitingPerson:
			if hasLead && !isLead {
				return &dashboardTurnState{State: turnWaitingWork, Reason: "@" + lead.name, Source: turnSourceJudge}
			}
			return &dashboardTurnState{State: turnWaitingPerson, Source: turnSourceJudge, TurnID: judgment.TurnID, Pushed: !judgment.PushedAt.IsZero(), Headline: shownLine(judgment.Headline, headlineShownRunes)}
		case turnDone, turnWaitingWork, turnContinuing:
			if judgment.Error == "" {
				return &dashboardTurnState{State: judgment.State, Source: turnSourceJudge}
			}
		}
	}
	if row.EndTurn != nil {
		return &dashboardTurnState{State: turnDone, Source: turnSourceEndTurn}
	}
	return nil
}

func namedSessions(ids []string, leads leadDirectory) string {
	if len(ids) == 1 {
		return leads.named(ids[0])
	}
	return fmt.Sprintf("%s and %s", leads.named(ids[0]), daemonapi.CountText(len(ids)-1, "other"))
}

func turnStateLabel(state *dashboardTurnState) string {
	switch state.State {
	case turnDone:
		return "Done"
	case turnWaitingPerson:
		return "Needs you"
	case turnContinuing:
		return "Said it would continue"
	case turnWaitingWork:
		if state.Reason != "" {
			return "Waiting on " + state.Reason
		}
		return "Waiting"
	}
	return ""
}
