package daemon

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestAHeldMessageWhoseDropIsNeverReportedFailsPastClaudesDeadline(t *testing.T) {
	for _, tc := range []struct {
		name, envTimeout, dialogExpiry string
		stillHeld, failed              time.Duration
	}{
		{name: "Claude's default deadline", stillHeld: 6 * time.Minute, failed: 8 * time.Minute},
		{name: "the timeout in the host's environment", envTimeout: "60000", stillHeld: 2 * time.Minute, failed: 4 * time.Minute},
		{name: "the config root's dialogExpiry", dialogExpiry: "10m", stillHeld: 11 * time.Minute, failed: 13 * time.Minute},
		{name: "a hold Claude keeps until answered", dialogExpiry: "never", stillHeld: 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_USER_DIALOG_TIMEOUT_MS", tc.envTimeout)
			home := t.TempDir()
			if tc.dialogExpiry != "" {
				roots, err := surface.ClaudeConfigRoots(home)
				if err != nil || len(roots) == 0 {
					t.Fatalf("roots=%v err=%v", roots, err)
				}
				if err := os.MkdirAll(roots[0], 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(surface.ClaudeSettingsPath(roots[0]), []byte(`{"dialogExpiry":"`+tc.dialogExpiry+`"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r := hostRegistry(t)
			for _, session := range []surface.Session{
				{ID: "sender", Name: "sender", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle},
				{ID: "target", Name: "probe", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", Cwd: filepath.Join(home, "work"), Status: surface.StatusIdle},
			} {
				if err := r.RegisterSession(session); err != nil {
					t.Fatal(err)
				}
			}
			intent, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: "sender", TargetSessionID: "target", Message: "held", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
			if err != nil {
				t.Fatal(err)
			}
			if held, err := r.HoldPeerMessage(registry.PeerMessage{MsgID: intent.UUID, TargetSessionID: "target"}, registry.PeerHeldReason("mode-mismatch")); err != nil || !held {
				t.Fatalf("held=%v err=%v", held, err)
			}
			held, err := r.HeldPeerMessages()
			if err != nil || len(held) != 1 {
				t.Fatalf("held=%+v err=%v", held, err)
			}
			d := newTestDaemon(t, r, []surface.Surface{surfaces.NewClaude(nil, home)})
			d.log = log.New(io.Discard, "", 0)
			status := func(after time.Duration) *registry.DeliveryIntent {
				t.Helper()
				d.failUnreportedPeerHolds(held[0].HeldAt.Add(after))
				got, err := r.DeliveryIntent(intent.ID)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			if got := status(tc.stillHeld); got.Status != registry.DeliveryIntentHeld {
				t.Fatalf("after %s: %+v, want still held", tc.stillHeld, got)
			}
			if tc.failed == 0 {
				return
			}
			if got := status(tc.failed); got.Status != registry.DeliveryIntentFailed || got.Failure != registry.PeerHoldUnreported {
				t.Fatalf("after %s: %+v, want failed as unreported", tc.failed, got)
			}
		})
	}
}
