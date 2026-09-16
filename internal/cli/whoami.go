package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type whoamiResult struct {
	Resolved bool   `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
	Session  string `json:"session,omitempty"`
	Surface  string `json:"surface,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Project  string `json:"project,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
}

func (a *App) cmdWhoami(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail whoami [--json]")
	}
	result := whoamiResult{}
	id, err := a.sourceSessionID(context.Background(), "")
	if err != nil {
		result.Reason = err.Error()
		return a.writeWhoami(result, hasFlag(args, "--json"))
	}
	if id == "" {
		result.Reason = "no caller session binding; set AGENTHAIL_SESSION_ID, CODEX_THREAD_ID, or CLAUDE_SESSION_ID"
		return a.writeWhoami(result, hasFlag(args, "--json"))
	}
	if a.Registry == nil {
		result.Reason = "caller session context is unavailable; run through the Agenthail CLI with its session registry"
		return a.writeWhoami(result, hasFlag(args, "--json"))
	}
	session, err := a.Registry.Session(id)
	if err != nil {
		result.Reason = fmt.Sprintf("caller session %q is not registered; run agenthail list from the caller session", id)
		return a.writeWhoami(result, hasFlag(args, "--json"))
	}
	result = whoamiResult{Resolved: true, Session: session.ID, Surface: string(session.Surface), Cwd: session.Cwd}
	if session.Cwd != "" {
		result.Project = filepath.Base(session.Cwd)
	}
	aliases, err := a.Registry.ListAliases()
	if err != nil {
		return fmt.Errorf("list caller aliases: %w", err)
	}
	for _, alias := range aliases {
		if alias.SessionID == session.ID {
			result.Alias = alias.Name
			break
		}
	}
	return a.writeWhoami(result, hasFlag(args, "--json"))
}

func (a *App) writeWhoami(result whoamiResult, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if !result.Resolved {
		fmt.Printf("unresolved: %s\n", result.Reason)
		return nil
	}
	fmt.Printf("session: %s\n", result.Session)
	fmt.Printf("surface: %s\n", result.Surface)
	if result.Alias != "" {
		fmt.Printf("alias: @%s\n", result.Alias)
	}
	fmt.Printf("project: %s\n", result.Project)
	fmt.Printf("cwd: %s\n", result.Cwd)
	return nil
}
