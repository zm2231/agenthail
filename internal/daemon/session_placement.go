package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type placementInputs struct {
	Archived      bool
	Status        surface.SessionStatus
	Working       bool
	WorkingBelow  int
	Queued        bool
	Waiting       bool
	WaitingSince  time.Time
	AsksPerson    bool
	HostDown      bool
	LimitResume   string
	UsageLimit    bool
	UsageResetsAt time.Time
	Pinned        bool
	Snooze        *dashboardSnooze
	TurnStop      string
	Current       bool
	CurrentReason string
	ProjectID     string
	ProjectName   string
	CheckoutID    string
	Subagent      *surface.Subagent
	Source        string
	Delegated     bool
	Surface       surface.SurfaceKind
	Search        string
	IdleAt        time.Time
}

func sessionPlacement(row dashboardSession) string {
	encoded, _ := json.Marshal(placementOf(row))
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:12])
}

// A busy row's lastActive is left out: the view never orders a busy family by it.
func placementOf(row dashboardSession) placementInputs {
	inputs := placementInputs{
		Archived:      row.Archived,
		Status:        row.Status,
		Working:       presentationWorking(&row),
		Queued:        row.QueueCount > 0,
		Waiting:       row.WaitingOn != nil,
		AsksPerson:    row.Ask.asksPerson(),
		HostDown:      mirroredHostDown(row),
		UsageLimit:    row.UsageLimit != nil,
		Pinned:        !row.PinnedAt.IsZero(),
		Snooze:        row.Snooze,
		Current:       row.Current,
		CurrentReason: row.CurrentReason,
		Subagent:      row.Subagent,
		Source:        row.Source,
		Delegated:     row.Delegated,
		Surface:       row.Surface,
		Search:        presentationSearch(row),
	}
	if row.WaitingOn != nil {
		inputs.WaitingSince = row.WaitingOn.Since.UTC()
	}
	if row.ObservedSubagents != nil {
		inputs.WorkingBelow = row.ObservedSubagents.Working
	}
	if row.LimitResume != nil {
		inputs.LimitResume = string(row.LimitResume.State)
	}
	if row.UsageLimit != nil {
		inputs.UsageResetsAt = row.UsageLimit.ResetsAt.UTC()
	}
	if row.TurnStop != nil {
		inputs.TurnStop = row.TurnStop.TurnID
	}
	if row.Project != nil {
		inputs.ProjectID, inputs.ProjectName = row.Project.ID, row.Project.DisplayName
	}
	if row.Checkout != nil {
		inputs.CheckoutID = row.Checkout.ID
	}
	if !inputs.Working && !inputs.Queued && !inputs.Waiting {
		inputs.IdleAt = row.LastActive.UTC()
	}
	return inputs
}
