package surface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	TurnSettingModel  = "model"
	TurnSettingEffort = "effort"
	TurnSettingMode   = "mode"
)

const (
	TurnSettingAppliesNow      = "now"
	TurnSettingAppliesNextTurn = "nextTurn"
)

// TurnSettingOption is one value a setting takes. Confirm marks a value that
// lets the agent act without asking, which a client confirms before choosing.
type TurnSettingOption struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Confirm bool   `json:"confirm,omitempty"`
}

// TurnSetting is one control a session offers for its turns. Applies says
// whether a change reaches the turn already running or only the next one.
type TurnSetting struct {
	Key     string              `json:"key"`
	Value   string              `json:"value"`
	Options []TurnSettingOption `json:"options"`
	Applies string              `json:"applies"`
	Scope   string              `json:"scope,omitempty"`
}

type TurnSettingsProvider interface {
	TurnSettings(ctx context.Context, sess *Session) ([]TurnSetting, error)
	SetTurnSetting(ctx context.Context, sess *Session, key, value string) error
}

type RuntimeVersionError struct {
	Runtime string
	Feature string
	Minimum string
}

func (e *RuntimeVersionError) Error() string {
	return fmt.Sprintf("%s needs %s %s or newer", e.Feature, e.Runtime, e.Minimum)
}

func (e *RuntimeVersionError) Is(target error) bool { return target == ErrUnsupported }

var ErrInvalidTurnSetting = errors.New("invalid turn setting")

func ValidTurnSetting(settings []TurnSetting, key, value string) error {
	for _, setting := range settings {
		if setting.Key != key {
			continue
		}
		for _, option := range setting.Options {
			if option.ID == value {
				return nil
			}
		}
		return fmt.Errorf("%w: %s does not offer %q", ErrInvalidTurnSetting, key, value)
	}
	return fmt.Errorf("%w: this session has no %s setting", ErrUnsupported, key)
}

func ValidModelName(name string) error {
	if name == "" || strings.ContainsFunc(name, unicode.IsSpace) {
		return fmt.Errorf("%w: a model is one model name", ErrInvalidTurnSetting)
	}
	return nil
}

func IsRuntimeVersionError(err error) (*RuntimeVersionError, bool) {
	var version *RuntimeVersionError
	return version, errors.As(err, &version)
}
