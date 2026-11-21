package voice

import (
	"encoding/json"
	"errors"
	"os"
)

func RequestTargets(statePath string) (map[string]bool, error) {
	data, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	for _, r := range state.Requests {
		targets[r.TargetID] = true
	}
	return targets, nil
}
