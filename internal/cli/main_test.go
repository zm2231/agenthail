package cli

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/simhost"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.StopGitAtTempDir()
	daemonStartBound = 20 * time.Second
	doctorSimulators = func(context.Context) *simhost.Status { return nil }
	os.Exit(testkit.RunInOwnHome(func() int {
		return testkit.RunAwayFromLiveDashboard(m.Run, daemon.StandardDashboardListen, &daemon.DefaultDashboardListen)
	}))
}
