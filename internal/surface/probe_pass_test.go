package surface

import (
	"context"
	"errors"
	"testing"
)

func TestProbeOnceSharesOneAnswerPerPass(t *testing.T) {
	probes := 0
	probe := func() (int, error) { probes++; return probes, errors.New("unreachable") }

	pass := WithProbePass(context.Background())
	for range 3 {
		if value, err := ProbeOnce(pass, "desktop", probe); value != 1 || err == nil {
			t.Fatalf("value=%d err=%v", value, err)
		}
	}
	if value, _ := ProbeOnce(pass, "other", probe); value != 2 {
		t.Fatalf("another key shared the first answer: %d", value)
	}
	if value, _ := ProbeOnce(WithProbePass(context.Background()), "desktop", probe); value != 3 {
		t.Fatalf("a new pass reused an old answer: %d", value)
	}
	if value, _ := ProbeOnce(context.Background(), "desktop", probe); value != 4 {
		t.Fatalf("a call outside a pass reused an answer: %d", value)
	}
}

func TestProbeOnceDoesNotShareAProbeItsCallerAbandoned(t *testing.T) {
	pass := WithProbePass(context.Background())
	abandoned, cancel := context.WithCancel(pass)
	_, _ = ProbeOnce(abandoned, "desktop", func() (string, error) {
		cancel()
		return "", context.Canceled
	})
	if value, err := ProbeOnce(pass, "desktop", func() (string, error) { return "found", nil }); value != "found" || err != nil {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestProbeOnceDoesNotShareAnAnswerItsCallerStoppedWaitingFor(t *testing.T) {
	pass := WithProbePass(context.Background())
	abandoned, cancel := context.WithCancel(pass)
	_, _ = ProbeOnce(abandoned, "desktop", func() (string, error) {
		cancel()
		return "stale", nil
	})
	if value, _ := ProbeOnce(pass, "desktop", func() (string, error) { return "fresh", nil }); value != "fresh" {
		t.Fatalf("a later call got the abandoned answer %q", value)
	}
}
