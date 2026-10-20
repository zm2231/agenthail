package surface

const TurnMetadataVersion = 1

const (
	TurnCompleted   = "completed"
	TurnInterrupted = "interrupted"
	TurnFailed      = "failed"
	TurnRunning     = "running"
	TurnUnknown     = "unknown"
)

// TurnMetadata is what the provider recorded about one turn. Unavailable
// names each field the provider did not record, so a missing value is never
// read as zero.
type TurnMetadata struct {
	V           int        `json:"v"`
	State       string     `json:"state"`
	Model       string     `json:"model,omitempty"`
	DurationMs  *int64     `json:"durationMs,omitempty"`
	Usage       *TurnUsage `json:"usage,omitempty"`
	Unavailable []string   `json:"unavailable,omitempty"`
}

// TurnUsage counts every model request in the turn. InputTokens includes the
// cached and cache-written input; OutputTokens includes the reasoning output.
type TurnUsage struct {
	InputTokens       int64  `json:"inputTokens"`
	CachedInputTokens int64  `json:"cachedInputTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	ReasoningTokens   *int64 `json:"reasoningTokens,omitempty"`
}

func NewTurnMetadata(state, model string, durationMs *int64, usage *TurnUsage) *TurnMetadata {
	turn := &TurnMetadata{V: TurnMetadataVersion, State: state, Model: model, DurationMs: durationMs, Usage: usage}
	if turn.State == "" {
		turn.State = TurnUnknown
	}
	if turn.State == TurnUnknown {
		turn.Unavailable = append(turn.Unavailable, "state")
	}
	if model == "" {
		turn.Unavailable = append(turn.Unavailable, "model")
	}
	if durationMs == nil {
		turn.Unavailable = append(turn.Unavailable, "durationMs")
	}
	switch {
	case usage == nil:
		turn.Unavailable = append(turn.Unavailable, "usage")
	case usage.ReasoningTokens == nil:
		turn.Unavailable = append(turn.Unavailable, "usage.reasoningTokens")
	}
	return turn
}

func TurnStateOfDone(status string) string {
	switch status {
	case "", "completed", "task_complete", "task_completed", "turn_completed":
		return TurnCompleted
	case "cancelled", "canceled", "interrupted", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted":
		return TurnInterrupted
	case "failed", "error", "systemerror":
		return TurnFailed
	}
	return TurnUnknown
}
