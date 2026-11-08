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
