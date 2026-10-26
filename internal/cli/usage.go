package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type usageMeterView struct {
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
	Aged     bool      `json:"aged"`
	Until    time.Time `json:"until,omitzero"`
}

type usageSessionView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Working bool   `json:"working"`
}

type usageSetView struct {
	Host     string             `json:"host"`
	HostID   string             `json:"hostId"`
	Local    bool               `json:"local"`
	Provider string             `json:"provider,omitempty"`
	Name     string             `json:"name,omitempty"`
	Account  string             `json:"account,omitempty"`
	Root     string             `json:"root,omitempty"`
	Primary  bool               `json:"primary,omitempty"`
	Meters   []usageMeterView   `json:"meters"`
	AsOf     time.Time          `json:"asOf,omitzero"`
	Problem  string             `json:"problem,omitempty"`
	Sessions []usageSessionView `json:"sessions"`
}

type usageSnapshotSession struct {
	ID       string `json:"id"`
	Surface  string `json:"surface"`
	Name     string `json:"name"`
	Alias    string `json:"alias"`
	Status   string `json:"status"`
	Current  bool   `json:"current"`
	Archived bool   `json:"archived"`
	Host     *struct {
		ID string `json:"id"`
	} `json:"host"`
	ClaudeConfigRoot *struct {
		Root string `json:"root"`
	} `json:"claudeConfigRoot"`
}

func (a *App) cmdUsage(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail usage [--json]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	var usage struct {
		Sets []usageSetView `json:"sets"`
	}
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/usage", nil, nil, &usage); err != nil {
		return err
	}
	var snapshot struct {
		Sessions []usageSnapshotSession `json:"sessions"`
	}
	if err := a.signinCall(ctx, http.MethodGet, "/api/v1/snapshot", nil, nil, &snapshot); err != nil {
		return err
	}
	for i := range usage.Sets {
		usage.Sets[i].Sessions = usageSessions(usage.Sets[i], snapshot.Sessions)
	}
	if hasFlag(args, "--json") {
		return emit(map[string]any{"sets": usage.Sets})
	}
	writeUsage(os.Stdout, usage.Sets, time.Now())
	return nil
}

func usageSessions(set usageSetView, sessions []usageSnapshotSession) []usageSessionView {
	views := []usageSessionView{}
	for _, session := range sessions {
		if session.Archived || session.Host == nil || session.Host.ID != set.HostID || session.Surface != set.Provider {
			continue
		}
		if set.Provider == "claude" && (session.ClaudeConfigRoot == nil || session.ClaudeConfigRoot.Root != set.Root) {
			continue
		}
		working := session.Status == "busy"
		if !working && !session.Current {
			continue
		}
		name := session.Name
		if session.Alias != "" {
			name = "@" + session.Alias
		}
		views = append(views, usageSessionView{ID: session.ID, Name: name, Working: working})
	}
	return views
}

func writeUsage(w io.Writer, sets []usageSetView, now time.Time) {
	host := ""
	for _, set := range sets {
		if set.HostID != host {
			host = set.HostID
			fmt.Fprintln(w, set.Host)
		}
		fmt.Fprintf(w, "  %s\n", usageSetTitle(set, now))
		for _, meter := range set.Meters {
			if !meter.Until.IsZero() && !now.Before(meter.Until) {
				continue
			}
			line := fmt.Sprintf("    %-12s %3.0f%%", usageKindLabel(meter), meter.Percent)
			if !meter.ResetsAt.IsZero() {
				line += "  resets in " + usageDuration(meter.ResetsAt.Sub(now))
			}
			if meter.Severity == "critical" || meter.Percent >= 90 {
				line += "  near its limit"
			}
			fmt.Fprintln(w, line)
		}
		if set.Problem != "" {
			fmt.Fprintf(w, "    %s\n", set.Problem)
		}
		if len(set.Sessions) > 0 {
			names := make([]string, 0, len(set.Sessions))
			for _, session := range set.Sessions {
				if session.Working {
					names = append(names, session.Name+" (working)")
				} else {
					names = append(names, session.Name)
				}
			}
			fmt.Fprintf(w, "    sessions: %s\n", strings.Join(names, ", "))
		}
	}
}

func usageSetTitle(set usageSetView, now time.Time) string {
	switch set.Provider {
	case "":
		return "(no answer)"
	case "codex":
		return "Codex"
	}
	title := set.Account
	if set.Name != "" && set.Name != set.Account {
		title += " (" + set.Name + ")"
	}
	if !set.AsOf.IsZero() {
		title += "  read " + usageDuration(now.Sub(set.AsOf)) + " ago"
	}
	return title
}

func usageKindLabel(meter usageMeterView) string {
	switch meter.Kind {
	case "session", "5h":
		return "session"
	case "weekly", "weekly_all":
		return "week"
	case "weekly_scoped":
		return "week, model"
	}
	return meter.Label
}

func usageDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	days, hours, minutes := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
