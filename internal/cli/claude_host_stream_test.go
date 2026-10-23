package cli

import (
	"context"
	"errors"
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
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	client := host.Start(t)
	const id = "6f3e1c2a-8d4b-4f5e-9a7c-1b2d3e4f5a6b"
	host.Open(t, client, id)
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
	output, err := captureStdout(t, func() error {
		go func() {
			time.Sleep(time.Second)
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return
			}
			defer file.Close()
			file.WriteString(`{"type":"user","uuid":"u2","message":{"content":"now"}}` + "\n" +
				`{"type":"assistant","uuid":"a2","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"the new reply"}]}}` + "\n")
		}()
		return app.Run([]string{"stream", id, "--timeout", "4s"})
	})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stream: %v (output %q)", err, output)
	}
	if !strings.Contains(output, "the new reply") || strings.Contains(output, "an earlier reply") {
		t.Fatalf("a direct stream prints only what is written after it starts: %q", output)
	}
}
