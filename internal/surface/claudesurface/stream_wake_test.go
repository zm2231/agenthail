package claudesurface

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

func claudeAssistantText(id, text string) string {
	return `{"type":"assistant","uuid":"` + id + `","message":{"id":"m-` + id + `","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestAClaudeStreamWakesOnAnAppendAndFollowsAReplacedTranscript(t *testing.T) {
	surfacetest.SlowPoll(t)
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"hello"}}`)
	session := seededClaudeStreamSession(t, path, "")
	waits := surfacetest.StreamWaits(t)
	texts := make(chan string, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(event surface.StreamEvent) {
			if event.Text != "" {
				texts <- event.Text
			}
		}, time.Minute)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	surfacetest.AwaitWaitBeyond(t, waits, 0)

	seen := waits.Load()
	appended := time.Now()
	testkit.AppendLines(t, path, claudeAssistantText("a1", "appended"))
	surfacetest.AwaitWokenText(t, texts, "appended", appended)
	surfacetest.AwaitWaitBeyond(t, waits, seen)

	seen = waits.Load()
	replaced := time.Now()
	surfacetest.ReplaceTranscript(t, path, `{"type":"user","uuid":"r1","message":{"content":"after the replacement"}}`)
	surfacetest.AwaitWokenText(t, texts, "after the replacement", replaced)
	surfacetest.AwaitWaitBeyond(t, waits, seen)

	later := time.Now()
	testkit.AppendLines(t, path, claudeAssistantText("a2", "appended to the replacement"))
	surfacetest.AwaitWokenText(t, texts, "appended to the replacement", later)
}
