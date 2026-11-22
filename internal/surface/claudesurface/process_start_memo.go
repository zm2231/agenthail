package claudesurface

import (
	"sync"
	"time"
)

// A pass checks each live record several times; a running process keeps its
// start time, and a reused pid is still caught once the memo lapses.
const processStartMemoWindow = time.Second

type processStartMemo struct {
	mu      sync.Mutex
	read    func(pid int) (time.Time, error)
	entries map[int]processStartEntry
}

type processStartEntry struct {
	started, readAt time.Time
}

var processStarts = &processStartMemo{read: processStartTime, entries: map[int]processStartEntry{}}

func (memo *processStartMemo) startTime(pid int, now time.Time) (time.Time, error) {
	memo.mu.Lock()
	for other, entry := range memo.entries {
		if now.Sub(entry.readAt) >= processStartMemoWindow {
			delete(memo.entries, other)
		}
	}
	entry, found := memo.entries[pid]
	memo.mu.Unlock()
	if found {
		return entry.started, nil
	}
	started, err := memo.read(pid)
	if err != nil {
		return started, err
	}
	memo.mu.Lock()
	memo.entries[pid] = processStartEntry{started: started, readAt: now}
	memo.mu.Unlock()
	return started, nil
}
