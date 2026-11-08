package surface

import (
	"context"
	"time"
)

type ClaudeRunObservation struct {
	RecordPath      string    `json:"recordPath"`
	JobID           string    `json:"jobId"`
	SessionID       string    `json:"sessionId,omitempty"`
	ResumeSessionID string    `json:"resumeSessionId,omitempty"`
	RunType         string    `json:"runType,omitempty"`
	ProviderState   string    `json:"providerState,omitempty"`
	CreatedAt       time.Time `json:"createdAt,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
}

// ClaudeSubagentLink is one Claude Code subagent of a session, observed from
// its transcript and the agent-<id>.meta.json Claude Code writes beside it.
type ClaudeSubagentLink struct {
	ParentSessionID string    `json:"parentSessionId"`
	AgentID         string    `json:"agentId"`
	AgentType       string    `json:"agentType,omitempty"`
	Description     string    `json:"description,omitempty"`
	ToolUseID       string    `json:"toolUseId,omitempty"`
	Depth           int       `json:"depth"`
	Working         bool      `json:"working"`
	StartedAt       time.Time `json:"startedAt,omitzero"`
	LastActive      time.Time `json:"lastActive,omitempty"`
	TranscriptPath  string    `json:"transcriptPath"`
}

type ClaudeRunObserver interface {
	ObserveClaudeRuns(context.Context) ([]ClaudeRunObservation, error)
	ObserveSessionClaudeRuns(ctx context.Context, session *Session) ([]ClaudeRunObservation, error)
	// ObserveClaudeSubagents lists the subagents of one Claude session.
	ObserveClaudeSubagents(ctx context.Context, session *Session) ([]ClaudeSubagentLink, error)
	ObserveAllClaudeSubagents(ctx context.Context) ([]ClaudeSubagentLink, error)
}

// With no running process (zero processStart) only the latest run is current.
func CurrentClaudeRuns(runs []ClaudeRunObservation, sessionID string, processStart time.Time) []ClaudeRunObservation {
	current := []ClaudeRunObservation{}
	var latest *ClaudeRunObservation
	for i, run := range runs {
		if run.SessionID != sessionID && run.ResumeSessionID != sessionID {
			continue
		}
		if !processStart.IsZero() {
			if !run.UpdatedAt.Before(processStart) {
				current = append(current, run)
			}
			continue
		}
		if latest == nil || run.UpdatedAt.After(latest.UpdatedAt) {
			latest = &runs[i]
		}
	}
	if latest != nil {
		current = append(current, *latest)
	}
	return current
}
