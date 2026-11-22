package surface

import (
	"os"
	"path/filepath"
	"strings"
)

// A config must name a binary that outlives this one: never a test or go run build, and Homebrew's opt link over its Cellar path.
func RelayExecutable() string {
	executable := AgenthailExecutable()
	if executable == "" || strings.Contains(executable, string(filepath.Separator)+"go-build") {
		return ""
	}
	marker := strings.Index(executable, "/Cellar/agenthail/")
	if marker <= 0 {
		return executable
	}
	linked := filepath.Join(executable[:marker], "opt", "agenthail", "bin", "agenthail")
	if _, err := os.Stat(linked); err == nil {
		return linked
	}
	return executable
}
