package surface

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

type TurnOptions struct {
	Effort       string          `json:"effort,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	ServiceTier  string          `json:"serviceTier,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

func (o TurnOptions) Empty() bool {
	return o.Effort == "" && o.Mode == "" && o.ServiceTier == "" && len(o.OutputSchema) == 0
}
func (o TurnOptions) Validate(kind SurfaceKind) error {
	if o.Empty() {
		return nil
	}
	if kind != KindCodex {
		return fmt.Errorf("advanced turn options require Codex")
	}
	if o.Effort != "" && !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, o.Effort) {
		return fmt.Errorf("invalid reasoning effort")
	}
	if o.Mode != "" && o.Mode != "plan" && o.Mode != "default" {
		return fmt.Errorf("mode must be plan or default")
	}
	if o.ServiceTier != "" && o.ServiceTier != "default" && o.ServiceTier != "fast" && o.ServiceTier != "flex" {
		return fmt.Errorf("service tier must be default, fast, or flex")
	}
	if len(o.OutputSchema) > 0 {
		var schema map[string]any
		if len(o.OutputSchema) > 64<<10 || json.Unmarshal(o.OutputSchema, &schema) != nil || schema == nil {
			return fmt.Errorf("output schema must be a JSON object, at most 64 KiB")
		}
	}
	return nil
}

type ForkOptions struct {
	Cwd          string `json:"cwd,omitempty"`
	Model        string `json:"model,omitempty"`
	BeforeTurnID string `json:"beforeTurnId,omitempty"`
	LastTurnID   string `json:"lastTurnId,omitempty"`
}
type SessionForker interface {
	ForkSession(context.Context, *Session, ForkOptions) (*Session, error)
}
type SessionLifecycle interface {
	SessionAction(context.Context, *Session, string) (map[string]any, error)
}
type NativeQueueRequest struct {
	Action   string   `json:"queueAction"`
	Message  string   `json:"message,omitempty"`
	ID       string   `json:"queuedSubmissionId,omitempty"`
	ClientID string   `json:"clientUserMessageId,omitempty"`
	IDs      []string `json:"queuedSubmissionIds,omitempty"`
	Cursor   string   `json:"cursor,omitempty"`
}
type NativeQueuer interface {
	NativeQueue(context.Context, *Session, NativeQueueRequest) (map[string]any, error)
}
