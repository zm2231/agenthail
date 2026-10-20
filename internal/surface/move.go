package surface

import (
	"context"
	"fmt"
	"time"
)

// MoveTarget is where a moved session runs next. Host is empty for this
// host; Account names a Claude config root as ResolveClaudeAccountRoot reads
// it, and an empty Account keeps the session's own root.
type MoveTarget struct {
	Host    string `json:"host,omitempty"`
	Account string `json:"account,omitempty"`
}

type MoveRequest struct {
	To MoveTarget `json:"to"`
	// StopTerminal is the person's consent to end an idle terminal process
	// that holds the conversation; without it such a move is refused.
	StopTerminal bool          `json:"stopTerminal,omitempty"`
	Wait         time.Duration `json:"wait,omitempty"`
}

type MoveResult struct {
	Session       *Session `json:"session"`
	FromRoot      string   `json:"fromRoot"`
	ToRoot        string   `json:"toRoot"`
	Stopped       string   `json:"stopped,omitempty"`
	Copied        bool     `json:"copied"`
	Unchanged     bool     `json:"unchanged,omitempty"`
	SameAccount   bool     `json:"sameAccount,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	RetiredSource string   `json:"retiredSource,omitempty"`
}

type SessionMover interface {
	MoveSession(ctx context.Context, sess *Session, request MoveRequest) (*MoveResult, error)
}

type MoveBusyError struct {
	Session string
	Reason  string
}

func (e *MoveBusyError) Error() string {
	return "Claude session " + e.Session + " is busy (" + e.Reason + "); move it when it is idle, or pass a wait"
}

type MoveConsentError struct {
	Session string
	PID     int
}

func (e *MoveConsentError) Error() string {
	return fmt.Sprintf("Claude session %s is open in a terminal (process %d); confirm stopping it to move it", e.Session, e.PID)
}
