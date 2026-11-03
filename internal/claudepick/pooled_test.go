package claudepick

import (
	"testing"
	"time"
)

func capped(percent, ceiling float64, resetsIn time.Duration) *Window {
	return &Window{Percent: percent, ResetsAt: now.Add(resetsIn), Ceiling: ceiling}
}

func TestAProxyShowsItsFreestOpenAccount(t *testing.T) {
	got := Pooled([]Member{
		{Account: "ops@example.com", Session: capped(50, 60, time.Hour), Weekly: capped(10, 60, 48*time.Hour)},
		{Account: "lead@example.com", Session: capped(6, 60, time.Hour), Weekly: capped(9, 60, 48*time.Hour)},
		{Account: "spare@example.com", Session: capped(1, 60, time.Hour), RetryAt: now.Add(time.Hour)},
	}, now)
	if got.Best != "lead@example.com" || got.Session.Percent != 6 || !got.FullUntil.IsZero() {
		t.Fatalf("the freest open account, never a cooling one: %+v", got)
	}
}

func TestAFullProxyShowsWhenItsFirstAccountFreesUp(t *testing.T) {
	got := Pooled([]Member{
		{Account: "ops@example.com", Session: capped(65, 60, 3*time.Hour)},
		{Account: "lead@example.com", Session: capped(10, 60, time.Hour), RetryAt: now.Add(2 * time.Hour)},
	}, now)
	if got.Best != "lead@example.com" || !got.FullUntil.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("65%% is over the proxy's own 60%% ceiling: %+v", got)
	}
	if got := Pooled(nil, now); got.Best != "" {
		t.Fatalf("%+v", got)
	}
}
