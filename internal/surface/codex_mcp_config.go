package surface

import (
	"encoding/json"
	"fmt"
)

func CodexMCPOff(home string) (bool, error) {
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return false, err
	}
	raw, ok := config["codexMCP"]
	if !ok {
		return false, nil
	}
	var on bool
	if err := json.Unmarshal(raw, &on); err != nil {
		return false, fmt.Errorf("codexMCP in %s must be true or false", AgenthailConfigPath(home))
	}
	return !on, nil
}
