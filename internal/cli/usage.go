package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type usageMeterView struct {
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Scope    string    `json:"scope,omitempty"`
	Percent  float64   `json:"percent"`
	Severity string    `json:"severity,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
	Aged     bool      `json:"aged"`
	Until    time.Time `json:"until,omitzero"`
	Ceiling  float64   `json:"ceiling,omitempty"`
}

type usageProxyView struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	URL  string `json:"url,omitempty"`
}

type usageAccountView struct {
	ID         string           `json:"id"`
	Provider   string           `json:"provider"`
	Label      string           `json:"label"`
	Status     string           `json:"status"`
	NextRetry  time.Time        `json:"nextRetry,omitzero"`
	Preference float64          `json:"preference"`
	Meters     []usageMeterView `json:"meters"`
	AsOf       time.Time        `json:"asOf,omitzero"`
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
	Login    string             `json:"login,omitempty"`
	Root     string             `json:"root,omitempty"`
	Primary  bool               `json:"primary,omitempty"`
	Meters   []usageMeterView   `json:"meters"`
	AsOf     time.Time          `json:"asOf,omitzero"`
	Problem  string             `json:"problem,omitempty"`
	Detail   string             `json:"problemDetail,omitempty"`
	Sessions []usageSessionView `json:"sessions"`

	Proxy        *usageProxyView    `json:"proxy,omitempty"`
	Accounts     []usageAccountView `json:"accounts,omitempty"`
	ProxyProblem string             `json:"proxyProblem,omitempty"`
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
	for i := range usage.Sets {
		if usage.Sets[i].Provider == "proxy" {
			usage.Sets[i].Sessions = proxySessions(usage.Sets[i], usage.Sets)
		}
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

func proxySessions(group usageSetView, sets []usageSetView) []usageSessionView {
	sessions := []usageSessionView{}
	for _, set := range sets {
		if set.Provider == "claude" && set.Proxy != nil && group.Proxy != nil && set.Proxy.ID == group.Proxy.ID {
			sessions = append(sessions, set.Sessions...)
		}
	}
	return sessions
}

func writeUsage(w io.Writer, sets []usageSetView, now time.Time) {
	host := ""
	for _, set := range sets {
		if set.HostID != host {
			host = set.HostID
			fmt.Fprintln(w, set.Host)
		}
		fmt.Fprintf(w, "  %s\n", usageSetTitle(set, now))
		if set.Provider == "proxy" {
			writeProxyAccounts(w, set, now, "    ")
			if len(set.Sessions) > 0 {
				fmt.Fprintf(w, "    sessions: %s\n", usageSessionLine(set.Sessions))
			}
			continue
		}
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
			fmt.Fprintf(w, "    sessions: %s\n", usageSessionLine(set.Sessions))
		}
	}
}

func usageSetTitle(set usageSetView, now time.Time) string {
	switch set.Provider {
	case "":
		return "(no answer)"
	case "codex":
		return "Codex"
	case "proxy":
		return set.Name + " (proxy)"
	}
	if set.Proxy != nil {
		return set.Name + " via the " + set.Proxy.Name + " proxy"
	}
	title := set.Account
	if title == "" {
		title = map[string]string{"api_key": "API key", "signed_out": "signed out"}[set.Login]
	}
	if title == "" {
		title = set.Name
	} else if set.Name != "" && set.Name != set.Account {
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
		if meter.Scope != "" {
			return "week, " + meter.Scope
		}
		return "week, one model"
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
