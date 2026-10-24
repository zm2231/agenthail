package cli

import (
	"encoding/json"
	"os"
)

func emit(value any) error {
	return json.NewEncoder(os.Stdout).Encode(value)
}
