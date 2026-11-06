package browserhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/installdir"
)

func InstalledLaunch(version string) (Launch, error) {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		return Launch{}, err
	}
	root := installdir.Of(executable)
	script := strings.TrimSpace(os.Getenv("AGENTHAIL_BROWSER_HOST"))
	if script == "" {
		script = filepath.Join(root, "browser-host", "host.mjs")
	}
	if _, err := os.Stat(script); err != nil {
		return Launch{}, fmt.Errorf("the browser host script is missing (set AGENTHAIL_BROWSER_HOST): %w", err)
	}
	node := installdir.Node(root)
	if _, err := os.Stat(node); err != nil {
		return Launch{}, fmt.Errorf("the Node runtime Agenthail ships is missing at %s; reinstall Agenthail, or for a source build run scripts/fetch-node.sh", node)
	}
	return Launch{Node: node, Script: script, Version: version, Env: os.Environ()}, nil
}
