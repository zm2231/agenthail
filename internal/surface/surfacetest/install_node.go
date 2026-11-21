package surfacetest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/installdir"
	"github.com/zm2231/agenthail/internal/surface"
)

// InstallNode stands script in for the Node runtime of an install at a
// temporary root, which the daemon runs in place of any node on PATH.
func InstallNode(t *testing.T, script string) string {
	t.Helper()
	root := t.TempDir()
	node := installdir.Node(root)
	if err := os.MkdirAll(filepath.Dir(node), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previous := surface.InstallRoot
	surface.InstallRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { surface.InstallRoot = previous })
	return node
}
