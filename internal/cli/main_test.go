package cli

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/claudetestenv"
)

func TestMain(m *testing.M) {
	claudetestenv.Clear()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID"} {
		os.Unsetenv(key)
	}
	os.Exit(m.Run())
}
