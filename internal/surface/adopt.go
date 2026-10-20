package surface

import (
	"context"
	"fmt"
	"time"
)

type AdoptRequest struct {
	// StopTerminal is the person's consent to end an idle terminal process
	// that holds the conversation; without it such an adoption is refused.
	StopTerminal bool          `json:"stopTerminal,omitempty"`
	Wait         time.Duration `json:"wait,omitempty"`
}

type AdoptResult struct {
	Session *Session `json:"session"`
	// Stopped names the process the adoption ended, such as a terminal
	// process or a background job.
	Stopped   string `json:"stopped,omitempty"`
	Unchanged bool   `json:"unchanged,omitempty"`
}

// SessionAdopter opens a session that runs elsewhere on this computer, such
// as in a terminal, in Agenthail's own host.
type SessionAdopter interface {
	AdoptSession(ctx context.Context, sess *Session, request AdoptRequest) (*AdoptResult, error)
}

type AdoptBusyError struct {
	Session string
	Reason  string
}

func (e *AdoptBusyError) Error() string {
	return "Claude session " + e.Session + " is busy (" + e.Reason + "); open it in Agenthail when it is idle, or pass a wait"
}

type AdoptConsentError struct {
	Session string
	PID     int
}

func (e *AdoptConsentError) Error() string {
	return fmt.Sprintf("Claude session %s is open in a terminal (process %d); confirm stopping it to open it in Agenthail", e.Session, e.PID)
}
