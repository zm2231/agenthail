package voice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
)

// Projects answers the project-aware voice tools. The daemon implements it
// with the rules @lead uses: a project is found by exact id or name, and its
// coordinators on two hosts are a conflict to resolve, never a pick.
type Projects interface {
	List(context.Context) ([]Project, error)
	// LeadSession is the exact local session of the project's coordinator.
	LeadSession(ctx context.Context, project string) (string, error)
	SendToLead(ctx context.Context, project, message, sourceSessionID string) (LeadReceipt, error)
}

type Project struct {
	Name         string `json:"name"`
	ID           string `json:"id"`
	Coordinators []Lead `json:"coordinators"`
	// Conflict is set when the project has coordinators on two hosts.
	Conflict bool `json:"conflict,omitempty"`
}

type Lead struct {
	Ref       string `json:"ref"`
	Host      string `json:"host"`
	SessionID string `json:"sessionId,omitempty"`
}

type LeadReceipt struct {
	Ref     string            `json:"ref"`
	Started bool              `json:"started,omitempty"`
	Receipt *delivery.Receipt `json:"receipt"`
}

// DynamicTools is the tool set registered when the operator's thread starts.
// Codex keeps a thread's dynamic tools from thread/start for its lifetime;
// thread/resume cannot change them.
func DynamicTools() []map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []map[string]any{{
		"type": "namespace", "name": "agenthail", "description": "Agenthail operations available to the persistent voice orchestrator.",
		"tools": []map[string]any{
			{"type": "function", "name": "voice_transfer", "description": "Transfer the connected voice call to one exact existing Codex or Claude session, or to a project's coordinator. Give exactly one of targetId or project.", "inputSchema": object(map[string]any{
				"targetId": text("Exact Agenthail session ID or alias."),
				"project":  text("Exact project name or id, as voice_projects lists it."),
			})},
			{"type": "function", "name": "voice_return_to_orchestrator", "description": "Return the connected voice call to Agenthail's normal orchestration workflow.", "inputSchema": object(map[string]any{})},
			{"type": "function", "name": "voice_projects", "description": "List the projects on this host and its paired hosts with their coordinators. A project marked conflict has coordinators on two hosts and cannot be reached until one is chosen with 'agenthail coordinator set --replace'.", "inputSchema": object(map[string]any{})},
			{"type": "function", "name": "voice_send_to_lead", "description": "Send a message to a project's coordinator, starting its conversation if it has none. Returns the delivery receipt, not a reply; read the reply with agenthail last and the returned ref.", "inputSchema": object(map[string]any{
				"project": text("Exact project name or id, as voice_projects lists it."),
				"message": text("The message to deliver."),
			}, "project", "message")},
		},
	}}
}

// ToolsDigest identifies DynamicTools, so a saved operator created with
// another tool set can be told apart.
func ToolsDigest() string {
	data, _ := json.Marshal(DynamicTools())
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

const toolsOutdatedNotice = "This voice conversation was created before Agenthail's current voice tools (projects and their coordinators). Choose New conversation to use them; Codex cannot add tools to an existing conversation."

// callTool runs one voice tool for the operator source. The caller holds no
// lock.
func (s *Service) callTool(ctx context.Context, tool string, arguments map[string]any, source string) (string, error) {
	text := func(key string) string {
		value, _ := arguments[key].(string)
		return strings.TrimSpace(value)
	}
	if actsForThePerson(tool) {
		if err := s.refuseMuted(); err != nil {
			return "", err
		}
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
			if s.projects == nil {
				return "", errors.New("project voice targets are unavailable on this host")
			}
			id, err := s.projects.LeadSession(ctx, project)
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
	case "voice_projects":
		if s.projects == nil {
			return "", errors.New("projects are unavailable on this host")
		}
		list, err := s.projects.List(ctx)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(map[string]any{"projects": list})
		return string(data), err
	case "voice_send_to_lead":
		project, message := text("project"), text("message")
		if project == "" || message == "" {
			return "", errors.New("a project and a message are required")
		}
		if s.projects == nil {
			return "", errors.New("projects are unavailable on this host")
		}
		receipt, err := s.projects.SendToLead(ctx, project, message, source)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(receipt)
		return string(data), err
	}
	return "", errors.New("unsupported Agenthail voice tool")
}

// actsForThePerson reports whether a tool acts on the person's behalf, so a
// muted call refuses it. Reading tools stay available.
func actsForThePerson(tool string) bool {
	switch tool {
	case "voice_transfer", "voice_return_to_orchestrator", "voice_send_to_lead":
		return true
	}
	return false
}

// leadSendTimeout bounds a send to a project lead, which may relay to a
// paired host.
const leadSendTimeout = 30 * time.Second

// SetProjects gives the operator's project tools their resolver. The daemon
// calls it once, before the service handles events.
func (s *Service) SetProjects(projects Projects) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects = projects
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
