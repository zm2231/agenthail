package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type cliController struct {
	*cliSurface
	evidence surface.DeliveryEvidence
	err      error
}

func (f *cliController) Control(_ context.Context, session *surface.Session, request surface.ControlRequest) (*surface.ControlResult, error) {
	return &surface.ControlResult{SessionID: session.ID, RequestID: "exact-request", Action: request.Action, Evidence: f.evidence, Source: "fixture-control", Detail: "observed evidence"}, f.err
}

func TestLegacyClaudeCommandsPreserveControlReceipts(t *testing.T) {
	for _, command := range []string{"model", "interrupt", "control"} {
		for _, unknown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/unknown=%t", command, unknown), func(t *testing.T) {
				fake := &cliSurface{kind: surface.KindClaude, caps: surface.Capabilities{Model: true, Interrupt: true}, sessions: map[string]surface.Session{"session_test": {ID: "session_test", Surface: surface.KindClaude, Status: surface.StatusBusy}}}
				app, _ := cliFixture(t, fake)
				controller := &cliController{cliSurface: fake, evidence: surface.EvidenceDelivered}
				if unknown {
					controller.evidence = surface.EvidenceUnknown
					controller.err = surface.DeliveryOutcomeUnknown(errors.New("confirmation missing"))
				}
				app.Surfaces = []SurfaceEntry{{Name: "claude", Surface: controller}}
				args := []string{command, "claude:session_test"}
				if command == "model" {
					args = append(args, "haiku")
				}
				if command == "control" {
					args = append(args, "interrupt")
				}
				args = append(args, "--json", "--timeout", "1s")
				output, err := captureStdout(t, func() error { return app.Run(args) })
				if unknown != surface.IsDeliveryOutcomeUnknown(err) {
					t.Fatalf("err=%v", err)
				}
				var receipt surface.ControlResult
				if err := json.Unmarshal([]byte(output), &receipt); err != nil {
					t.Fatal(err)
				}
				if receipt.RequestID != "exact-request" || receipt.Evidence != controller.evidence || receipt.Detail == "" {
					t.Fatalf("receipt=%+v", receipt)
				}
			})
		}
	}
}
