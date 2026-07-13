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
		{"vscode", "notLoaded", true, codexTransportDesktop},
		{"cli", "idle", true, codexTransportManaged},
		{"cli", "notLoaded", true, codexTransportReadOnly},
		{"cli", "idle", false, codexTransportReadOnly},
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

func TestReadOnlySessionCoversLegacyRowsWithoutBlockingDesktop(t *testing.T) {
	legacy := &surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle}
	unclassifiedDesktopSource := &surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode"}
	desktop := &surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"}
	if !surface.IsReadOnlySession(legacy) {
		t.Fatal("legacy unloaded session was writable")
	}
	if !surface.IsReadOnlySession(unclassifiedDesktopSource) {
		t.Fatal("blank transport was writable")
	}
	if surface.IsReadOnlySession(desktop) {
		t.Fatal("Desktop session was read only")
	}
}
