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
	HostDown      bool
	LimitResume   string
	UsageLimit    bool
	UsageResetsAt time.Time
	Current       bool
	CurrentReason string
	ProjectID     string
	ProjectName   string
	CheckoutID    string
	ParentID      string
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
		HostDown:      mirroredHostDown(row),
		UsageLimit:    row.UsageLimit != nil,
		Current:       row.Current,
		CurrentReason: row.CurrentReason,
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
	if row.Project != nil {
		inputs.ProjectID, inputs.ProjectName = row.Project.ID, row.Project.DisplayName
	}
	if row.Checkout != nil {
		inputs.CheckoutID = row.Checkout.ID
	}
	if row.Subagent != nil {
		inputs.ParentID = row.Subagent.ParentID
	}
	if !inputs.Working && !inputs.Queued && !inputs.Waiting {
		inputs.IdleAt = row.LastActive.UTC()
	}
	return inputs
}
