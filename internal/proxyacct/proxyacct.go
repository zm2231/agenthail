package proxyacct

import (
	"context"
	"errors"
	"time"
)

type Proxy interface {
	Health(ctx context.Context) error
	Accounts(ctx context.Context) ([]Account, error)
	SetPreference(ctx context.Context, account string, preference float64) error
	ReleaseSessions(ctx context.Context, account string) error
}

const (
	Preferred    float64 = 10
	NotPreferred float64 = 0
)

const (
	StatusActive      = "active"
	StatusCooling     = "cooling"
	StatusDisabled    = "disabled"
	StatusUnavailable = "unavailable"
)

const (
	WindowSession   = "session"
	WindowWeek      = "week"
	WindowWeekModel = "week_model"
)

type Account struct {
	ID         string    `json:"id"`
	Provider   string    `json:"provider"`
	Label      string    `json:"label"`
	Status     string    `json:"status"`
	NextRetry  time.Time `json:"nextRetry,omitzero"`
	Preference float64   `json:"preference"`
	Windows    []Window  `json:"windows"`
	Drain      []Drain   `json:"-"`
}

// Drain is one window as CLIProxyAPI's quota-drain selector reads it: from
// the credential-wide headers, with the per-model week filled from the newest
// model state only when the credential lacks it. A reading may hold a reset
// without a percent; a Ceiling of 0 is none.
type Drain struct {
	Kind       string
	Percent    float64
	HasPercent bool
	ResetsAt   time.Time
	Ceiling    float64
}

type Window struct {
	Kind       string    `json:"kind"`
	Percent    float64   `json:"percent"`
	ResetsAt   time.Time `json:"resetsAt,omitzero"`
	ObservedAt time.Time `json:"observedAt,omitzero"`
	Ceiling    float64   `json:"ceiling,omitempty"`
}

type Problem string

const (
	Unreachable   Problem = "unreachable"
	KeyMissing    Problem = "key_missing"
	KeyUnreadable Problem = "key_unreadable"
	KeyRefused    Problem = "key_refused"
	Banned        Problem = "banned"
	RemoteOff     Problem = "remote_off"
	NoManagement  Problem = "no_management"
	ReadOnly      Problem = "read_only"
	NoAccount     Problem = "no_account"
	UnknownAnswer Problem = "unknown_answer"
)

// Error never carries a key value, only what went wrong and the cause.
type Error struct {
	Problem Problem
	Err     error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return string(e.Problem)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func ProblemOf(err error) Problem {
	var proxyErr *Error
	if errors.As(err, &proxyErr) {
		return proxyErr.Problem
	}
	if err == nil {
		return ""
	}
	return UnknownAnswer
}
