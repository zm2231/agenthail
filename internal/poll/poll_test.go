package poll

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUntil(t *testing.T) {
	calls := 0
	if err := Until(context.Background(), time.Millisecond, func() (bool, error) {
		calls++
		return calls == 2, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUntilReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Until(ctx, time.Millisecond, func() (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("Until() = %v, want context cancellation", err)
	}
}
