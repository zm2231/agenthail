package github

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	os.Exit(testkit.RunInOwnHome(m.Run))
}
