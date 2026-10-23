// Package testnode finds the Node runtime Agenthail ships, staged in the
// checkout by scripts/fetch-node.sh, for tests that run JavaScript.
package testnode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zm2231/agenthail/internal/installdir"
)

func Path(t testing.TB) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	node := installdir.Node(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(node); err != nil {
		t.Fatalf("tests run the Node Agenthail ships; stage it with scripts/fetch-node.sh: %v", err)
	}
	return node
}
