package surface

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSourceSessionIDContextAndSendOptionsJSON(t *testing.T) {
	ctx := WithSourceSessionID(context.Background(), "claude-source")
	if got := SourceSessionID(ctx); got != "claude-source" {
		t.Fatalf("source session id=%q", got)
	}
	if got := SourceSessionID(context.Background()); got != "" {
		t.Fatalf("background source session id=%q", got)
	}
	payload, err := json.Marshal(SendOptions{SourceSessionID: "claude-source"})
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"sourceSessionId":"claude-source"}` {
		t.Fatalf("options JSON=%s", payload)
	}
}
