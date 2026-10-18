package daemon

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Exit(m.Run())
}
