package nodehost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/installdir"
)

// InstalledLaunch runs dir/host.mjs from this install on the Node it ships;
// envOverride names a variable that points at another script.
func InstalledLaunch(name, dir, envOverride, version string) (Launch, error) {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		return Launch{}, err
	}
	root := installdir.Of(executable)
	script := strings.TrimSpace(os.Getenv(envOverride))
	if script == "" {
		script = filepath.Join(root, dir, "host.mjs")
	}
	if _, err := os.Stat(script); err != nil {
		return Launch{}, fmt.Errorf("the %s script is missing (set %s): %w", name, envOverride, err)
	}
	node := installdir.Node(root)
	if _, err := os.Stat(node); err != nil {
		return Launch{}, fmt.Errorf("the Node runtime Agenthail ships is missing at %s; reinstall Agenthail, or for a source build run scripts/fetch-node.sh", node)
	}
	return Launch{Node: node, Script: script, Version: version, Env: os.Environ()}, nil
}
