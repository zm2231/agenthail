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
	V          int        `json:"v"`
	State      string     `json:"state"`
	Model      string     `json:"model,omitempty"`
	DurationMs *int64     `json:"durationMs,omitempty"`
	Usage      *TurnUsage `json:"usage,omitempty"`
	// ContextTokens is how much of the context window the conversation filled
	// when the turn ended.
	ContextTokens *int64 `json:"contextTokens,omitempty"`
	// Compactions lists, oldest first, each compaction from the end of the
	// previous turn to the end of this one. Absent and not unavailable means
	// there were none.
	Compactions []TurnCompaction `json:"compactions,omitempty"`
	Unavailable []string         `json:"unavailable,omitempty"`
}

const (
	CompactionIdle      = "idle"
	CompactionThreshold = "threshold"
	CompactionManual    = "manual"
	CompactionUnknown   = "unknown"
)

// TurnCompaction is one compaction of the conversation: what triggered it and
// the context it left. A size the provider did not record is absent.
type TurnCompaction struct {
	Trigger    string `json:"trigger"`
	PreTokens  *int64 `json:"preTokens,omitempty"`
	PostTokens *int64 `json:"postTokens,omitempty"`
}

// TurnMeasure is what a provider measured about a turn. A nil field was not
// measured; a nil Compactions means the provider could not tell, an empty one
// that there were none.
type TurnMeasure struct {
	Model         string
	DurationMs    *int64
	Usage         *TurnUsage
	ContextTokens *int64
	Compactions   []TurnCompaction
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

func NewTurnMetadata(state string, measure TurnMeasure) *TurnMetadata {
	turn := &TurnMetadata{V: TurnMetadataVersion, State: state, Model: measure.Model, DurationMs: measure.DurationMs, Usage: measure.Usage, ContextTokens: measure.ContextTokens}
	if len(measure.Compactions) > 0 {
		turn.Compactions = measure.Compactions
	}
	if turn.State == "" {
		turn.State = TurnUnknown
	}
	if turn.State == TurnUnknown {
		turn.Unavailable = append(turn.Unavailable, "state")
	}
	if measure.Model == "" {
		turn.Unavailable = append(turn.Unavailable, "model")
	}
	if measure.DurationMs == nil {
		turn.Unavailable = append(turn.Unavailable, "durationMs")
	}
	switch {
	case measure.Usage == nil:
		turn.Unavailable = append(turn.Unavailable, "usage")
	case measure.Usage.ReasoningTokens == nil:
		turn.Unavailable = append(turn.Unavailable, "usage.reasoningTokens")
	}
	if measure.ContextTokens == nil {
		turn.Unavailable = append(turn.Unavailable, "contextTokens")
	}
	if measure.Compactions == nil {
		turn.Unavailable = append(turn.Unavailable, "compactions")
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
