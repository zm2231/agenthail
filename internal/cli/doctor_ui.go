package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/uiconf"
)

func (a *App) uiConfProblems() ([]uiconf.Problem, error) {
	text, err := os.ReadFile(filepath.Join(a.uiDir(), uiconf.FileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return uiconf.Parse(string(text)).Problems, nil
}
