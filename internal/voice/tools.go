package voice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Leads finds a project's coordinator for a transfer, with the rules @lead
// uses: a project is found by exact id or name, and its coordinators on two
// hosts are a conflict to resolve, never a pick.
type Leads interface {
	// LeadSession is the exact local session of the project's coordinator.
	LeadSession(ctx context.Context, project string) (string, error)
}

// DynamicTools is the tool set registered when the operator's thread starts.
// Codex keeps a thread's dynamic tools from thread/start for its lifetime;
// thread/resume cannot change them.
func DynamicTools() []map[string]any {
	object := func(properties map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	}
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []map[string]any{{
		"type": "namespace", "name": "agenthail", "description": "Voice call controls for the persistent Agenthail orchestrator.",
		"tools": []map[string]any{
			{"type": "function", "name": "voice_transfer", "description": "Transfer the connected voice call to one exact existing Codex or Claude session, or to a project's coordinator. Give exactly one of targetId or project.", "inputSchema": object(map[string]any{
				"targetId": text("Exact Agenthail session ID or alias."),
				"project":  text("Exact project name or id, as agenthail_projects lists it."),
			})},
			{"type": "function", "name": "voice_return_to_orchestrator", "description": "Return the connected voice call to Agenthail's normal orchestration workflow.", "inputSchema": object(map[string]any{})},
		},
	}}
}

// ToolsDigest identifies DynamicTools, so a saved operator created with
// another tool set can be told apart.
func ToolsDigest() string {
	data, _ := json.Marshal(DynamicTools())
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

const toolsOutdatedNotice = "This voice conversation was created before Agenthail's current tools: it still works sessions through the agenthail command line. Choose New conversation to use Agenthail's MCP tools; Codex cannot change an existing conversation's tools."

// callTool runs one voice tool for the operator. The caller holds no lock.
func (s *Service) callTool(ctx context.Context, tool string, arguments map[string]any) (string, error) {
	text := func(key string) string {
		value, _ := arguments[key].(string)
		return strings.TrimSpace(value)
	}
	if err := s.refuseMuted(); err != nil {
		return "", err
	}
	switch tool {
	case "voice_transfer":
		targetID, project := text("targetId"), text("project")
		if (targetID == "") == (project == "") {
			return "", errors.New("give exactly one of targetId or project")
		}
		// Finding a project's lead is part of the transfer, so it keeps its
		// place among the call's actions.
		s.op.Lock()
		defer s.op.Unlock()
		if err := s.refuseMuted(); err != nil {
			return "", err
		}
		if project != "" {
			if s.leads == nil {
				return "", errors.New("project voice targets are unavailable on this host")
			}
			id, err := s.leads.LeadSession(ctx, project)
			if err != nil {
				return "", err
			}
			targetID = id
		}
		name, err := s.selectTarget(ctx, targetID, transferBySpeech)
		if err != nil {
			return "", err
		}
		return "Voice transferred to " + name + ".", nil
	case "voice_return_to_orchestrator":
		s.op.Lock()
		_, err := s.selectTarget(ctx, "", transferBySpeech)
		s.op.Unlock()
		if err != nil {
			return "", err
		}
		return "Voice is now with the Agenthail orchestrator.", nil
	}
	return "", errors.New("unsupported Agenthail voice tool")
}

// SetLeads gives the transfer tool its project lead resolver. The daemon
// calls it once, before the service handles events.
func (s *Service) SetLeads(leads Leads) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leads = leads
}

// SetLog gives the service the daemon's log. The daemon calls it once, before
// the service handles events.
func (s *Service) SetLog(logf func(format string, args ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf = logf
}

// SetBriefer gives session-bound calls the reader of their session's brief.
// The daemon calls it once, before the service handles events.
func (s *Service) SetBriefer(briefer Briefer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.briefer = briefer
}

func (s *Service) toolsOutdated() bool {
	return s.state.State.Session != nil && s.state.State.ToolsDigest != ToolsDigest() && !s.state.ToolsNoticeShown
}
