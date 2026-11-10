package claudehosttest

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The fake Claude writes its session record under CLAUDE_CONFIG_DIR, as the
// real one does, so a test binary started from a Claude session would write
// into that session's configuration unless its TestMain drops the variable.
func TestAnInheritedClaudeConfigDirNeverReachesTheFixture(t *testing.T) {
	inherited := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHostsExitOnceTheTestProcessThatOwnsThemIsGone$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+inherited, "CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the fixture's own test failed under an inherited Claude session: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(inherited)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the fixture wrote into the inherited CLAUDE_CONFIG_DIR: %v", names)
	}
}
