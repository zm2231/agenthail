package surfaces

import (
	"os"
	"path/filepath"
)

// AgenthailExecutable is the running agenthail binary, or "" when this
// process is not one, such as a test binary, which must never run as a hook.
func AgenthailExecutable() string {
	executable, err := os.Executable()
	if err != nil || filepath.Base(executable) != "agenthail" {
		return ""
	}
	return executable
}
