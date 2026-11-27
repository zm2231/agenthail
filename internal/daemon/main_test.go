package daemon

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	os.Exit(testkit.RunInOwnHome(func() int {
		return testkit.RunAwayFromLiveDashboard(m.Run, dashconfig.StandardListen, &dashconfig.DefaultListen)
	}))
}
