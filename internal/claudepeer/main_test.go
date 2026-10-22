package claudepeer

import (
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/claudetestenv"
)

func TestMain(m *testing.M) {
	claudetestenv.Clear()
	os.Exit(m.Run())
}
