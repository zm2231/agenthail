package proxyacct

import (
	"context"
	"errors"
	"time"
)

type Proxy interface {
	Health(ctx context.Context) error
	Accounts(ctx context.Context) ([]Account, error)
}

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
