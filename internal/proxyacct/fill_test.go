package proxyacct

import (
	"testing"
	"time"
)

func TestAProxyFillsAsItsLeastFullAccountAndFreesAtTheFirstReset(t *testing.T) {
	now := time.Now()
	drain := func(session, week float64, resets time.Time) []Drain {
		return []Drain{
			{Kind: WindowSession, Percent: session, HasPercent: true, ResetsAt: resets, Ceiling: 80},
			{Kind: WindowWeek, Percent: week, HasPercent: true, ResetsAt: now.Add(100 * time.Hour)},
		}
	}
	fuller := Account{ID: "fuller", Provider: "claude", Status: StatusActive, Drain: drain(76, 20, now.Add(4*time.Hour))}
	roomier := Account{ID: "roomier", Provider: "claude", Status: StatusActive, Drain: drain(20, 92, now.Add(2*time.Hour))}
	cooling := Account{ID: "cooling", Provider: "claude", Status: StatusActive, NextRetry: now.Add(time.Hour)}
	disabled := Account{ID: "off", Provider: "claude", Status: StatusDisabled}
	codex := Account{ID: "codex", Provider: "codex", Status: StatusActive}

	percent, resets, ok := Fill([]Account{fuller, roomier, cooling, disabled, codex}, now)
	if !ok || percent != 92 || !resets.Equal(now.Add(time.Hour)) {
		t.Fatalf("fill = %v %v %v, want 92%% (the roomier account's week) freeing at the cooling account's retry", percent, resets, ok)
	}
	stale := Account{ID: "stale", Provider: "claude", Status: StatusActive, Drain: []Drain{{Kind: WindowSession, Percent: 99, HasPercent: true}}}
	if percent, resets, ok := Fill([]Account{fuller, stale}, now); !ok || percent != 95 || !resets.IsZero() {
		t.Fatalf("fill = %v %v %v, want 95%% of the ceiling with no known reset", percent, resets, ok)
	}
	if _, _, ok := Fill([]Account{disabled, codex}, now); ok {
		t.Fatal("a proxy with no usable Claude account has no fill")
	}
}
