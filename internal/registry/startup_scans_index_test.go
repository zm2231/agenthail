package registry

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestASchema119RegistryUpgradesToIndexedPidReleaseAndCurrentJudgmentReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`DROP INDEX IF EXISTS sessions_pid`, `PRAGMA user_version=119`} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, step := range queryPlan(t, r, releaseClaudePIDSQL, "claude", 4242, "s1") {
		if strings.Contains(step, "sessions") && !strings.Contains(step, "USING INDEX sessions_pid") {
			t.Errorf("releasing a Claude pid reads sessions without the pid index: %q", step)
		}
	}
	for _, step := range queryPlan(t, r, currentTurnJudgmentsQuery) {
		if strings.HasPrefix(step, "SCAN j") {
			t.Errorf("current judgments scan every judgment: %q", step)
		}
	}
}
