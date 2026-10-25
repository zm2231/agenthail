package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type SessionDisplay struct {
	Kind          string     `json:"kind"`
	Label         string     `json:"label"`
	Detail        string     `json:"detail"`
	WaitingPlace  string     `json:"waitingPlace,omitempty"`
	ResetsAt      *time.Time `json:"resetsAt,omitempty"`
	ResumesAt     *time.Time `json:"resumesAt,omitempty"`
	QuietSince    *time.Time `json:"quietSince,omitempty"`
	ToolStartedAt *time.Time `json:"toolStartedAt,omitempty"`
}

func displayInstant(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}

func sessionDisplay(row dashboardSession, now time.Time) SessionDisplay {
	display := sessionDisplayState(row, now)
	if row.Freshness != nil && row.Freshness.Stale {
		if display.Detail != "" {
			display.Detail += " · "
		}
		display.Detail += "Last observation is stale"
	}
	return display
}

func sessionDisplayState(row dashboardSession, now time.Time) SessionDisplay {
	if row.Host != nil && row.Host.State == "unreachable" || row.Status == surface.StatusUnknown || row.Status == "notLoaded" {
		return SessionDisplay{Kind: "unavailable", Label: "Unavailable", Detail: row.UnavailableReason}
	}
	if wait := row.WaitingOn; wait != nil {
		place, detail := "elsewhere", "Answer where the session is running"
		switch {
		case wait.Answerable:
			place, detail = "agenthail", "Answer in Agenthail"
		case wait.Kind == surface.WaitingDialog:
			place, detail = "dialog", "A dialog is open in its terminal"
		case !row.ReadOnly && row.Capabilities.Lifecycle.Stop && row.Capabilities.Lifecycle.Resume:
			place, detail = "restart", "Prompt out of reach; stop and resume to clear it"
		case row.RuntimeKind == surface.RuntimeClaudeCLI:
			place, detail = "terminal", "Answer in its terminal"
		}
		return SessionDisplay{Kind: "waiting", Label: "Waiting on you", Detail: detail, WaitingPlace: place}
	}
	if row.Status == surface.StatusBusy {
		display := SessionDisplay{Kind: "working", Label: "Working"}
		if activity := row.Activity; activity != nil {
			last := activity.LastProgressAt
			if last.IsZero() {
				last = activity.BusySince
			}
			if !last.IsZero() && now.Sub(last) >= 10*time.Minute {
				display.Detail = "No activity since"
				display.QuietSince = displayInstant(last)
				if activity.NoProcess {
					display.Detail += " · No Claude process"
				} else if tool := activity.Tool; tool != nil {
					display.Detail += " · Running " + tool.Name
					display.ToolStartedAt = displayInstant(tool.StartedAt)
				}
			}
		}
		return display
	}
	if stop := row.LimitResume; stop != nil && stop.State != registry.LimitResumeReleased {
		display := SessionDisplay{Kind: "limited", Label: "Usage limit"}
		switch stop.State {
		case registry.LimitResumeWaiting:
			display.Detail = "Resumes when the limit lifts"
			if !stop.ResetsAt.IsZero() {
				display.Detail = "Automatic resume scheduled"
				display.ResumesAt = displayInstant(stop.ResetsAt)
			}
		case registry.LimitResumeCanceled:
			display.Detail = "Stopped; automatic resume canceled"
		case registry.LimitResumeLapsed:
			display.Detail = "Stopped; automatic resume expired"
		}
		return display
	}
	if limit := row.UsageLimit; limit != nil && !limit.Lifted(now) {
		detail := "Reset time unknown"
		if !limit.ResetsAt.IsZero() {
			detail = "Limit reset scheduled"
		}
		return SessionDisplay{Kind: "limited", Label: "Usage limit", Detail: detail, ResetsAt: displayInstant(limit.ResetsAt)}
	}
	if surface.BackgroundRunning(row.BackgroundTasks) {
		return SessionDisplay{Kind: "finishing", Label: "Finishing", Detail: "Waiting on background work"}
	}
	if row.QueueCount > 0 {
		return SessionDisplay{Kind: "queued", Label: "Queued", Detail: fmt.Sprintf("%d messages waiting", row.QueueCount)}
	}
	if row.Status == surface.StatusOffline {
		return SessionDisplay{Kind: "offline", Label: "Offline"}
	}
	label := "Closed"
	if row.Open {
		label = "Ready"
	}
	return SessionDisplay{Kind: "idle", Label: label}
}

func presentationWorking(row dashboardSession) bool {
	return row.Status == surface.StatusBusy || surface.BackgroundRunning(row.BackgroundTasks) || row.ObservedSubagents != nil && row.ObservedSubagents.Working > 0
}

func presentationSearch(row dashboardSession) string {
	parts := []string{row.ID, row.Name, row.Alias, string(row.Surface), row.Cwd}
	if row.Project != nil {
		parts = append(parts, row.Project.DisplayName)
	}
	if row.Checkout != nil {
		parts = append(parts, row.Checkout.Path, row.Checkout.Branch)
	}
	if row.Host != nil {
		parts = append(parts, row.Host.Name)
	}
	if row.Subagent != nil {
		parts = append(parts, row.Subagent.Nickname)
	}
	return strings.ToLower(strings.Join(parts, " "))
}
