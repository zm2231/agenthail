package nodehost

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestASocketPathOverThePlatformLimitIsRefusedBeforeTheHostStarts(t *testing.T) {
	path := func(length int) string { return "/" + strings.Repeat("s", length-1) }
	for _, check := range []struct {
		goos   string
		length int
		ok     bool
	}{
		{"darwin", 103, true},
		{"darwin", 104, false},
		{"linux", 104, true},
		{"linux", 107, true},
		{"linux", 108, false},
	} {
		err := checkSocketPath(path(check.length), check.goos)
		if (err == nil) != check.ok {
			t.Fatalf("%s, %d bytes: err = %v, want ok %v", check.goos, check.length, err, check.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "shorter home directory") {
			t.Fatalf("%s: the refusal does not say what to change: %v", check.goos, err)
		}
	}

	down := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Supervisor{
		Name:       "test host",
		StateDir:   t.TempDir(),
		Socket:     path(200),
		Launch:     func() (Launch, error) { return Launch{Node: "/bin/false"}, nil },
		Down:       func(err error) { cancel(); down <- err },
		MinRestart: time.Hour,
	}
	go s.Run(ctx)
	select {
	case err := <-down:
		if err == nil || !strings.Contains(err.Error(), "200 bytes") {
			t.Fatalf("Down got %v, want the socket path refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the supervisor never reported the refused socket path")
	}
}
