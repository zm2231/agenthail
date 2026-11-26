// Package agentswitch is the person's choice of whether Agenthail works with
// an agent whose program runs on this computer: automatic (when it is
// installed), always on, or off. A missing program is a failure only for an
// agent the person turned on.
package agentswitch

import (
	"errors"
	"fmt"
)

type Mode string

const (
	Auto Mode = "auto"
	On   Mode = "on"
	Off  Mode = "off"
)

var Modes = []Mode{Auto, On, Off}

func ParseMode(value string) (Mode, error) {
	for _, mode := range Modes {
		if string(mode) == value {
			return mode, nil
		}
	}
	return "", fmt.Errorf("an agent's setting is auto, on or off, not %q", value)
}

type Agent struct {
	ID     string
	Label  string
	Locate func() error
}

type OffError struct {
	Agent string
	Label string
}

func (e OffError) Error() string {
	return fmt.Sprintf("%s is turned off; to turn it back on, run `agenthail agents use %s auto` or open Settings > Agents", e.Label, e.Agent)
}

type UnavailableError struct {
	Agent string
	Label string
	Err   error
}

func (e UnavailableError) Error() string {
	return fmt.Sprintf("%s is not installed on this computer (%s); install it to use it here, or run `agenthail agents use %s off` to turn it off", e.Label, e.Err, e.Agent)
}

func (e UnavailableError) Unwrap() error { return e.Err }

type MissingError struct {
	Agent string
	Label string
	Err   error
}

func (e MissingError) Error() string {
	return fmt.Sprintf("%s is turned on but not installed on this computer (%s); install it, or run `agenthail agents use %s off`", e.Label, e.Err, e.Agent)
}

func (e MissingError) Unwrap() error { return e.Err }

func IsOff(err error) bool {
	var target OffError
	return errors.As(err, &target)
}

func IsUnavailable(err error) bool {
	var target UnavailableError
	return errors.As(err, &target)
}

func IsMissing(err error) bool {
	var target MissingError
	return errors.As(err, &target)
}

// Check reads probeErr, the outcome of reaching the agent here, through the
// person's setting. An agent on auto is unavailable only when it cannot be
// reached and its program is missing, since some agents are reached without
// their program on PATH.
func Check(agent Agent, mode Mode, probeErr error) error {
	if mode == Off {
		return OffError{Agent: agent.ID, Label: agent.Label}
	}
	err := agent.Locate()
	switch {
	case err == nil:
		return probeErr
	case mode == On:
		return MissingError{Agent: agent.ID, Label: agent.Label, Err: err}
	case probeErr == nil:
		return nil
	default:
		return UnavailableError{Agent: agent.ID, Label: agent.Label, Err: err}
	}
}
