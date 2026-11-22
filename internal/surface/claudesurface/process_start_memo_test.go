package claudesurface

import (
	"errors"
	"testing"
	"time"
)

func TestAPassReadsAProcessStartOnceAndAReusedPidIsSeenOnceTheMemoLapses(t *testing.T) {
	first, reused := time.Unix(1_700_000_000, 0), time.Unix(1_700_000_500, 0)
	started, reads := first, 0
	memo := &processStartMemo{read: func(int) (time.Time, error) { reads++; return started, nil }, entries: map[int]processStartEntry{}}
	now := time.Now()
	for check := 0; check < 4; check++ {
		if got, err := memo.startTime(42, now.Add(time.Duration(check)*100*time.Millisecond)); err != nil || !got.Equal(first) {
			t.Fatalf("check %d: started=%v err=%v", check, got, err)
		}
	}
	if reads != 1 {
		t.Fatalf("four checks within one pass read the start time %d times, want once", reads)
	}
	started = reused
	if got, _ := memo.startTime(42, now.Add(processStartMemoWindow)); !got.Equal(reused) {
		t.Fatalf("after the memo lapsed the start time is %v, want the reused pid's %v", got, reused)
	}
}

func TestAProcessThatExitedLeavesNoStartTimeBehind(t *testing.T) {
	failing := false
	memo := &processStartMemo{read: func(int) (time.Time, error) {
		if failing {
			return time.Time{}, errors.New("no such process")
		}
		return time.Unix(1_700_000_000, 0), nil
	}, entries: map[int]processStartEntry{}}
	now := time.Now()
	for _, pid := range []int{41, 42} {
		if _, err := memo.startTime(pid, now); err != nil {
			t.Fatal(err)
		}
	}
	failing = true
	if _, err := memo.startTime(43, now.Add(processStartMemoWindow)); err == nil {
		t.Fatal("a failed read returned no error")
	}
	if len(memo.entries) != 0 {
		t.Fatalf("expired start times kept after a failed read: %v", memo.entries)
	}
}
