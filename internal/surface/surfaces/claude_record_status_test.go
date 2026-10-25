package surfaces

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestClaudeWaitingBeforeTheFirstTranscript(t *testing.T) {
	for _, test := range []struct {
		waitingFor string
		kind       surface.WaitingKind
	}{
		{"dialog open", surface.WaitingDialog},
		{"input needed", surface.WaitingQuestion},
		{"permission prompt", surface.WaitingApproval},
	} {
		t.Run(test.waitingFor, func(t *testing.T) {
			fixture := claudetest.New(t)
			fixture.Listen()
			cwd := t.TempDir()
			asked := time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC)
			fixture.RecordStatus("conversation", cwd, "", "waiting", test.waitingFor, asked)
			claude := NewClaude(nil, fixture.Home)
			ctx := context.Background()
			sessions, err := claude.List(ctx)
			if err != nil || len(sessions) != 1 {
				t.Fatalf("sessions=%+v err=%v", sessions, err)
			}
			want := surface.Waiting{Kind: test.kind, ID: fmt.Sprintf("conversation:%s:%d", test.kind, asked.UnixMilli()), Since: asked}
			listed := sessions[0]
			if listed.HasLocal || listed.Transcript != "" {
				t.Fatalf("fresh session has a transcript: %+v", listed)
			}
			if listed.Status != surface.StatusBusy || listed.WaitingOn == nil || *listed.WaitingOn != want {
				t.Errorf("listed status=%s waiting=%+v; want busy, %+v", listed.Status, listed.WaitingOn, want)
			}
			updated, err := claude.LocalStatus(ctx, listed)
			if err != nil || updated.Status != surface.StatusBusy || updated.WaitingOn == nil || *updated.WaitingOn != want {
				t.Errorf("local status=%s waiting=%+v err=%v; want busy, %+v", updated.Status, updated.WaitingOn, err, want)
			}
			fixture.RecordStatus("conversation", cwd, "", "idle", "", asked.Add(time.Second))
			cleared, err := claude.LocalStatus(ctx, updated)
			if err != nil || cleared.Status != surface.StatusIdle || cleared.WaitingOn != nil {
				t.Fatalf("closed dialog status=%s waiting=%+v err=%v", cleared.Status, cleared.WaitingOn, err)
			}
		})
	}
}
