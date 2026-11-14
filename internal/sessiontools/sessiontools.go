// Package sessiontools says whether a session has Agenthail's MCP tools, so
// what it is taught and what it is told agree with what it can call.
package sessiontools

import (
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/claudemcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// Of is GivenTools for a session holding a live credential for the server
// Agenthail gave it, and OwnTools for one Agenthail never gave a server whose
// own config starts the stdio relay, outside a project with the tools off.
// project names the session's project, or "" for none; it is read only for
// a session without a credential. Of reads the relay from this process's
// config, so it is for a process the session itself runs.
func Of(reg *registry.Registry, session surface.Session, project func() (string, error)) agenttools.Access {
	return of(reg, session, project, false)
}

// Has is Of for the daemon, whose Codex home may not be the one a Codex
// thread runs from: it counts a thread's relay only when the thread's rollout
// is in the daemon's home.
func Has(reg *registry.Registry, session surface.Session, project func() (string, error)) bool {
	return of(reg, session, project, true) != agenttools.NoTools
}

func of(reg *registry.Registry, session surface.Session, project func() (string, error), outside bool) agenttools.Access {
	if _, live, err := reg.LiveCredentialHash(session.ID, time.Now()); err != nil || live {
		if err != nil {
			return agenttools.NoTools
		}
		return agenttools.GivenTools
	}
	if issued, err := reg.SessionCredentialIssued(session.ID); err != nil || issued {
		return agenttools.NoTools
	}
	if !relayConfigured(session, outside) {
		return agenttools.NoTools
	}
	projectID, err := project()
	if err != nil {
		return agenttools.NoTools
	}
	if projectID != "" {
		if value, err := reg.ProjectSettingValue(projectID, registry.ProjectSettingMCP); err != nil || value == registry.ProjectMCPOff {
			return agenttools.NoTools
		}
	}
	return agenttools.OwnTools
}

func relayConfigured(session surface.Session, outside bool) bool {
	switch {
	case session.Surface == surface.KindCodex:
		home := surfaces.CodexHomeFromEnv()
		return (!outside || home.HoldsThread(session)) && surfaces.CodexRelayConfigured(home)
	case session.Surface == surface.KindClaude && session.RuntimeKind == surface.RuntimeClaudeCLI:
		home, _ := os.UserHomeDir()
		for _, root := range surface.ClaudeSessionRoots(session) {
			if state, err := claudemcp.StateAt(surface.ClaudeGlobalConfigPath(home, root), ""); err == nil && (state == claudemcp.Installed || state == claudemcp.Outdated) {
				return true
			}
		}
	}
	return false
}
