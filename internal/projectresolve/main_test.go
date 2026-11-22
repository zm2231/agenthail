package projectresolve

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.StopGitAtTempDir()
	os.Exit(testkit.RunInOwnHome(m.Run))
}
