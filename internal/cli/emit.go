package cli

import (
	"os"

	"github.com/zm2231/agenthail/internal/apitime"
)

func emit(value any) error {
	encoded, err := apitime.Marshal(value)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(encoded, '\n'))
	return err
}
