package codexsurface

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexCreationRefusesAClaudeConfigRoot(t *testing.T) {
	c := &Codex{}
	if _, _, err := c.startSessionOnTransport(context.Background(), nil, surface.SessionStartOptions{Message: "hi", ConfigRoot: "work"}, codexTransportManaged); err == nil || !strings.Contains(err.Error(), "require Claude") {
		t.Fatalf("err=%v", err)
	}
}
