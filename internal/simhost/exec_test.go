package simhost

import (
	"context"
	"testing"
	"time"
)

func TestACancelledCommandReturnsWhileItsChildHoldsTheOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := RunCommand(ctx, Command{Name: "sh", Args: []string{"-c", "sleep 30 & wait"}}); err == nil {
		t.Fatal("a cancelled command reported success")
	}
	if waited := time.Since(started); waited > 10*time.Second {
		t.Fatalf("the cancelled command took %s to return", waited)
	}
}
