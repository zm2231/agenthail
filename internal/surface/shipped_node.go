package surface

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/installdir"
)

// ShippedNode is the Node runtime Agenthail ships. A node on PATH is never
// used: its version is whatever the machine happens to have.
func ShippedNode() (string, error) {
	root, err := InstallRoot()
	if err != nil {
		return "", err
	}
	node := installdir.Node(root)
	if _, err := os.Stat(node); err != nil {
		return "", fmt.Errorf("the Node runtime Agenthail ships is missing at %s; reinstall Agenthail, or for a source build run scripts/fetch-node.sh", node)
	}
	return node, nil
}

var InstallRoot = func() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		return "", err
	}
	return installdir.Of(executable), nil
}
