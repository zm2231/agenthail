package registry

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckingWhetherAClaudePidIsAttachedElsewhereUsesThePidIndex(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for s := 0; s < 300; s++ {
		if _, err := tx.Exec(`INSERT INTO sessions (id,surface,pid) VALUES (?,'claude',?)`, fmt.Sprintf("session-%d", s), s%3*(1000+s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, step := range queryPlan(t, r, claudePIDAttachedElsewhereSQL, "claude", 1001, "session-1") {
		if strings.Contains(step, "sessions") && !strings.Contains(step, "USING INDEX sessions_pid") {
			t.Errorf("checking a Claude pid reads sessions without the pid index: %q", step)
		}
	}
	var attached bool
	if err := r.read.QueryRow(claudePIDAttachedElsewhereSQL, "claude", 1001, "session-2").Scan(&attached); err != nil || !attached {
		t.Fatalf("pid 1001 held by session-1 is not seen from session-2: %v %v", attached, err)
	}
}
