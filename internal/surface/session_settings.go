package surface

import "context"

// Where a reported setting was read from. runtime is the running process
// itself; rollout is Codex's last turn_context; transcript is Claude's last
// prompt record; started is what Agenthail opened the session with.
const (
	SettingFromRuntime    = "runtime"
	SettingFromRollout    = "rollout"
	SettingFromTranscript = "transcript"
	SettingFromStarted    = "started"
)

type SettingValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

// ModelSetting reports Listed when the runtime's model list was read: whether
// the value is one of its ids. A model the runtime resolved from an alias, or
// a custom id, is unlisted.
type ModelSetting struct {
	Value  string `json:"value"`
	Source string `json:"source"`
	Listed *bool  `json:"listed,omitempty"`
}

type PermissionSetting struct {
	Mode           string `json:"mode,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	Source         string `json:"source"`
}

// RuntimeSettings is what a session's runtime reports about how it runs. A
// field it cannot read is nil, with the reason in Unavailable keyed by the
// field's JSON name.
type RuntimeSettings struct {
	Model             *ModelSetting
	Effort            *SettingValue
	Permission        *PermissionSetting
	CollaborationMode *SettingValue
	Unavailable       map[string]string
}

func (s *RuntimeSettings) Missing(field, why string) {
	if s.Unavailable == nil {
		s.Unavailable = map[string]string{}
	}
	s.Unavailable[field] = why
}

// SessionSettingsReader reads a session's effective settings without waking,
// resuming or writing to it.
type SessionSettingsReader interface {
	SessionSettings(ctx context.Context, sess *Session) (RuntimeSettings, error)
}

// CodexPeerClass is how a Codex session runs in the terms of Claude's
// cross-session policy: bypass only when it neither asks for approval nor runs
// in a sandbox, prompting otherwise, and "" when neither is known.
func CodexPeerClass(permission *PermissionSetting) string {
	switch {
	case permission == nil || permission.ApprovalPolicy == "" && permission.Sandbox == "":
		return ""
	case permission.ApprovalPolicy == "never" && permission.Sandbox == "danger-full-access":
		return "bypass"
	}
	return "prompting"
}
