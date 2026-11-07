package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestStreamWithoutADaemonFollowsAHostedSessionFromItsEnd(t *testing.T) {
	began := time.Now()
	stamp := func(what string) { t.Logf("%s at +%s", what, time.Since(began).Round(time.Millisecond)) }
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	client := host.Start(t)
	stamp("host started")
	const id = "6f3e1c2a-8d4b-4f5e-9a7c-1b2d3e4f5a6b"
	host.Open(t, client, id)
	stamp("host opened")
	app, _ := claudeCLIFixture(t, surfaces.NewClaude(nil, fixture.Home))
	path := filepath.Join(fixture.Home, ".claude", "projects", "a-project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	history := `{"type":"user","uuid":"u1","message":{"content":"earlier"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"an earlier reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	appended := make(chan int, 1)
	output, err := captureStdout(t, func() error {
		// Turns keep landing while the stream runs, so some land after it starts however long that takes.
		go func() {
			turns := 0
			defer func() { appended <- turns }()
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return
			}
			defer file.Close()
			for {
				select {
				case <-ran:
					return
				case <-time.After(100 * time.Millisecond):
				}
				turns++
				fmt.Fprintf(file, `{"type":"user","uuid":"u-%d","message":{"content":"now"}}`+"\n"+
					`{"type":"assistant","uuid":"a-%d","message":{"id":"m-%d","stop_reason":"end_turn","content":[{"type":"text","text":"new reply %d"}]}}`+"\n", turns, turns, turns, turns)
			}
		}()
		stamp("stream started")
		defer stamp("stream ended")
		defer close(ran)
		return app.Run([]string{"stream", id, "--timeout", "4s"})
	})
	stamp(fmt.Sprintf("%d turns appended", <-appended))
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream: %v (output %q)", err, output)
	}
	if !strings.Contains(output, "new reply") || strings.Contains(output, "an earlier reply") {
		t.Fatalf("a direct stream prints only what is written after it starts: %q", output)
	}
}
