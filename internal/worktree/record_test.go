package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestConcurrentWorktreesForOneBranchMakeOneAndRefuseTheRest(t *testing.T) {
	checkout := repository(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	const racers = 6
	var wg sync.WaitGroup
	plans := make([]Plan, racers)
	errs := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plans[i], errs[i] = Begin(context.Background(), reg, Spec{Cwd: checkout, Branch: "ah/shared"})
		}()
	}
	wg.Wait()
	made, winner := 0, Plan{}
	for i, err := range errs {
		var refused RefusedError
		switch {
		case err == nil:
			made, winner = made+1, plans[i]
			if _, statErr := os.Stat(plans[i].WorktreePath); statErr != nil {
				t.Fatalf("the worktree that was made is gone: %v", statErr)
			}
		case !errors.As(err, &refused):
			t.Fatalf("a losing create failed instead of being refused: %v", err)
		}
	}
	if made != 1 {
		t.Fatalf("%d creates made the worktree, want 1: %v", made, errs)
	}
	if work, found, err := reg.WorkAt(winner.WorktreePath); err != nil || !found || work.Branch != "ah/shared" {
		t.Fatalf("the made worktree's record: %+v found=%v err=%v", work, found, err)
	}
}

func TestParallelWorkersEachGetTheirOwnWorktree(t *testing.T) {
	checkout := repository(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	const workers = 6
	var wg sync.WaitGroup
	plans := make([]Plan, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plans[i], errs[i] = Begin(context.Background(), reg, Spec{Cwd: checkout, Name: fmt.Sprintf("@worker%d", i)})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if _, err := os.Stat(plans[i].WorktreePath); err != nil {
			t.Fatalf("worker %d has no worktree: %v", i, err)
		}
		if !branchExists(t, checkout, plans[i].Branch) {
			t.Fatalf("worker %d has no branch %s", i, plans[i].Branch)
		}
	}
}
