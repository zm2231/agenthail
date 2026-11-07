package surface

import (
	"errors"
	"fmt"
	"slices"
)

type LaunchSettings struct {
	Effort string `json:"effort,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

func (s LaunchSettings) Empty() bool {
	return s.Effort == "" && s.Mode == ""
}

func (s LaunchSettings) ValidateCodex() error {
	if s.Effort != "" && !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, s.Effort) {
		return fmt.Errorf("invalid reasoning effort")
	}
	if s.Mode != "" && s.Mode != "plan" && s.Mode != "default" {
		return fmt.Errorf("mode must be plan or default")
	}
	return nil
}

var ErrTurnSettingsOnSend = errors.New("effort and mode are session settings: change them with the turn-setting action, not with a message")
