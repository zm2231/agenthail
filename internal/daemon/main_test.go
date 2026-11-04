package daemon

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.ClearAgentEnv()
	os.Exit(testkit.RunAwayFromLiveDashboard(m.Run, StandardDashboardListen, &DefaultDashboardListen))
}
