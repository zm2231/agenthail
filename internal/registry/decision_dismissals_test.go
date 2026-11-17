package registry

import (
	"testing"
	"time"
)

func TestADecisionDismissalKeepsOnlyTheNewestStatePerSession(t *testing.T) {
	r := openTestRegistry(t)
	if dismissed, err := r.DecisionDismissals(); err != nil || len(dismissed) != 0 {
		t.Fatalf("fresh registry dismissals=%v err=%v", dismissed, err)
	}
	for _, key := range []string{"stopped\x1fold\x1f1", "stopped\x1fnew\x1f2"} {
		if err := r.DismissDecision("s1", key, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DismissDecision("s2", "usageLimit\x1fx\x1f3", time.Now()); err != nil {
		t.Fatal(err)
	}
	dismissed, err := r.DecisionDismissals()
	if err != nil || len(dismissed) != 2 || dismissed["s1"] != "stopped\x1fnew\x1f2" || dismissed["s2"] != "usageLimit\x1fx\x1f3" {
		t.Fatalf("dismissals=%q err=%v", dismissed, err)
	}
}
