package claudehost_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
)

func TestAnOpenWhoseHelloGoesUnansweredIsKnownNotToHaveOpened(t *testing.T) {
	home, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	client := claudehost.NewClient(home)
	if err := os.MkdirAll(filepath.Dir(client.Socket), 0o700); err != nil {
		t.Fatal(err)
	}
	stalled := make(chan struct{})
	t.Cleanup(func() { close(stalled) })
	var opens atomic.Int32
	stubHost(t, client.Socket, func(method string) (any, *claudehost.Error) {
		if method == "open" {
			opens.Add(1)
			return claudehost.Session{}, nil
		}
		<-stalled
		return hello, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = client.Open(ctx, claudehost.OpenRequest{SessionID: uuid.NewString(), Cwd: home})
	var unknown *claudehost.OutcomeUnknownError
	if err == nil || errors.As(err, &unknown) || opens.Load() != 0 {
		t.Fatalf("err=%v opens=%d, want a failure that is known to have opened nothing", err, opens.Load())
	}
}
