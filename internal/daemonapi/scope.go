package daemonapi

import "encoding/json"

const ScopePath = "/api/v1/scope"

type ScopeRequest struct {
	SessionID      string          `json:"sessionId"`
	Action         string          `json:"action"`
	Target         string          `json:"target,omitempty"`
	Project        string          `json:"project,omitempty"`
	Mode           string          `json:"mode,omitempty"`
	Channel        string          `json:"channel,omitempty"`
	Surface        string          `json:"surface,omitempty"`
	PermissionMode string          `json:"permissionMode,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	Payload        *PersonAction   `json:"payload,omitempty"`
	AskPerson      bool            `json:"askPerson,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Create         json.RawMessage `json:"create,omitempty"`
	Notice         string          `json:"notice,omitempty"`
}

type ScopeReply struct {
	TargetID       string `json:"targetId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	ScopeProjectID string `json:"scopeProjectId,omitempty"`
	AskID          string `json:"askId,omitempty"`
	Notice         string `json:"notice,omitempty"`
}
