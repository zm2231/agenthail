package acp_test

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/acp/acptest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	acptest.RunIfAgent()
	os.Exit(testkit.RunInOwnHome(m.Run))
}
