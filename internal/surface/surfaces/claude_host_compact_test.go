package surfaces

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestACompactOfAHostSessionWaitsForItsBoundaryOrSaysWhyClaudeRefused(t *testing.T) {
	c, fixture := hostedClaude(t)
	c.localOnly = true
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), sess, "hello", nil); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the turn's reply in the transcript", 15*time.Second, func() bool {
		data, _ := os.ReadFile(c.transcriptPath(sess))
		return strings.Contains(string(data), `"type":"assistant"`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Compact(ctx, sess); err != nil {
		t.Fatalf("compact: %v", err)
	}
	data, err := os.ReadFile(c.transcriptPath(sess))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"subtype":"compact_boundary"`) != 1 {
		t.Fatalf("transcript after compact:\n%s\nwant one compact boundary", data)
	}
	err = c.Compact(ctx, sess)
	if !surface.IsDeliveryTerminal(err) || !strings.Contains(err.Error(), "No messages to compact") {
		t.Fatalf("compact with nothing to compact: err = %v, want Claude's refusal as a final failure", err)
	}
	if ctx.Err() != nil {
		t.Fatal("a refused compact waited out its deadline instead of reading Claude's error")
	}
}
