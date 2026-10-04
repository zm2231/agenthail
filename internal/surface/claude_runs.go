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

type ClaudeSubagentLink struct {
	ParentSessionID string `json:"parentSessionId"`
	AgentID         string `json:"agentId"`
	TranscriptPath  string `json:"transcriptPath"`
}

type ClaudeRunObserver interface {
	ObserveClaudeRuns(context.Context) ([]ClaudeRunObservation, error)
	ObserveClaudeSubagentLinks(context.Context) ([]ClaudeSubagentLink, error)
}
