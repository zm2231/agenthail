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
	// NotSteered is the notSteered of the reply to the action the notice is
	// for, unset when that reply had none.
	NotSteered *bool `json:"notSteered,omitempty"`
	// Ran is the action the command ran on the scoped target, for a notice
	// held before the command named it.
	Ran string `json:"ran,omitempty"`
	// ControlDecision is the decision the owning host reported applying to
	// the action, unset when that host did not report one.
	ControlDecision string `json:"controlDecision,omitempty"`
}

type ScopeReply struct {
	TargetID       string `json:"targetId,omitempty"`
	ProjectID      string `json:"projectId,omitempty"`
	ScopeProjectID string `json:"scopeProjectId,omitempty"`
	ScopeRank      string `json:"scopeRank,omitempty"`
	ScopeOverseen  string `json:"scopeOverseen,omitempty"`
	ScopeControl   string `json:"scopeControl,omitempty"`
	AskID          string `json:"askId,omitempty"`
	Notice         string `json:"notice,omitempty"`
}
