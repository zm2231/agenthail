package surfaces

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexTransportSeparatesDesktopManagedAndPlainCLI(t *testing.T) {
	cases := []struct {
		source  string
		status  any
		managed bool
		want    string
	}{
		{"vscode", "idle", false, codexTransportDesktop},
		{"cli", "idle", true, codexTransportManaged},
		{"cli", "idle", false, codexTransportReadOnly},
		{"vscode", "notLoaded", false, codexTransportReadOnly},
	}
	for _, test := range cases {
		if got := codexTransport(test.source, test.status, test.managed); got != test.want {
			t.Fatalf("source=%s status=%v managed=%v got=%s want=%s", test.source, test.status, test.managed, got, test.want)
		}
	}
}

func TestCodexRejectsMutationsForPlainTerminalSession(t *testing.T) {
	codex := NewCodex("http://127.0.0.1:1")
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "cli", Transport: codexTransportReadOnly}
	_, err := codex.Send(context.Background(), session, "do not deliver")
	if err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("err=%v", err)
	}
}
