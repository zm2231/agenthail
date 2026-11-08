package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

const threadSettingsUsage = "usage: agenthail thread settings <target> [--json]"

type threadSettingValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
	Listed *bool  `json:"listed"`
}

type threadSettings struct {
	SessionID  string `json:"sessionId"`
	Surface    string `json:"surface"`
	ConfigRoot *struct {
		Name       string `json:"name"`
		Root       string `json:"root"`
		Source     string `json:"source"`
		Missing    bool   `json:"missing"`
		SignedInAs string `json:"signedInAs"`
	} `json:"configRoot"`
	Model      *threadSettingValue `json:"model"`
	Effort     *threadSettingValue `json:"effort"`
	Permission *struct {
		Mode           string `json:"mode"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        string `json:"sandbox"`
		Source         string `json:"source"`
	} `json:"permission"`
	CollaborationMode *threadSettingValue `json:"collaborationMode"`
	Ceiling           *struct {
		Permission string   `json:"permission"`
		Roots      []string `json:"roots"`
	} `json:"ceiling"`
	Unavailable map[string]string `json:"unavailable"`
}

func (a *App) cmdThreadSettings(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 2 {
		return fmt.Errorf("%s", threadSettingsUsage)
	}
	ctx, cancel, err := a.deadlineContext(a.DefaultTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	id, err := a.settingsTarget(ctx, positional[1])
	if err != nil {
		return err
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.getJSON(ctx, "/api/v1/session-settings", url.Values{"id": []string{id}}, &raw); err != nil {
		return fmt.Errorf("read the settings of %s: %w", positional[1], err)
	}
	if hasFlag(args, "--json") {
		return emit(raw)
	}
	var settings threadSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("decode the settings of %s: %w", positional[1], err)
	}
	fmt.Print(formatThreadSettings(settings))
	return nil
}

func (a *App) settingsTarget(ctx context.Context, target string) (string, error) {
	if a.registry() != nil {
		if id, err := a.registry().ResolveTarget(target); err == nil {
			return id, nil
		}
	}
	session, _, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return "", err
	}
	if remote != nil {
		return remote.ID, nil
	}
	return session.ID, nil
}

func formatThreadSettings(s threadSettings) string {
	var b strings.Builder
	line := func(label, value, source string) {
		if source != "" {
			value += "  (" + source + ")"
		}
		fmt.Fprintf(&b, "%-12s %s\n", label, value)
	}
	fmt.Fprintf(&b, "%s %s\n", s.Surface, s.SessionID)
	if s.Model != nil {
		value := s.Model.Value
		if s.Model.Listed != nil && !*s.Model.Listed {
			value += " [custom]"
		}
		line("model", value, s.Model.Source)
	}
	if s.Effort != nil {
		line("effort", s.Effort.Value, s.Effort.Source)
	}
	if p := s.Permission; p != nil {
		var parts []string
		for _, part := range []string{p.Mode, p.ApprovalPolicy, p.Sandbox} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		line("permission", strings.Join(parts, ", "), p.Source)
	}
	if s.CollaborationMode != nil {
		line("mode", s.CollaborationMode.Value, s.CollaborationMode.Source)
	}
	if r := s.ConfigRoot; r != nil {
		value := r.Name + "  " + r.Root
		if r.SignedInAs != "" {
			value += "  " + r.SignedInAs
		}
		if r.Missing {
			value += "  [does not exist]"
		}
		line("config root", value, claudeRootSourceLabel(r.Source))
	}
	if c := s.Ceiling; c != nil {
		value := c.Permission
		if len(c.Roots) > 0 {
			value = strings.TrimSpace(value + " in " + strings.Join(c.Roots, ", "))
		}
		line("ceiling", value, "")
	}
	fields := make([]string, 0, len(s.Unavailable))
	for field := range s.Unavailable {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		fmt.Fprintf(&b, "%-12s unknown: %s\n", field, s.Unavailable[field])
	}
	return b.String()
}
