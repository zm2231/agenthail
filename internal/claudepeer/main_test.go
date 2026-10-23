package claudepeer

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.ClearAgentEnv()
	os.Exit(m.Run())
}
