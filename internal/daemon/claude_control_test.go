package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type dashboardController struct {
	*daemonSurface
	unknown bool
}

func (f *dashboardController) Control(_ context.Context, session *surface.Session, request surface.ControlRequest) (*surface.ControlResult, error) {
	result := &surface.ControlResult{SessionID: session.ID, RequestID: "request-proof", Action: request.Action, Evidence: surface.EvidenceDelivered, Source: "fixture"}
	if f.unknown {
		result.Evidence = surface.EvidenceUnknown
		return result, surface.DeliveryOutcomeUnknown(errors.New("response missing"))
	}
	return result, nil
}

func TestDashboardClaudeControlsKeepReceiptsOnUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		d, r, fake, session, _ := daemonFixture(t)
		fake.kind = surface.KindClaude
		fake.caps = surface.Capabilities{Model: true, Interrupt: true}
		session.Surface = surface.KindClaude
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		d = New(r, []surface.Surface{&dashboardController{daemonSurface: fake, unknown: unknown}})
		response := httptest.NewRecorder()
		apiV1JSONHandler(d.dashboardActionHandler)(response, httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewBufferString(`{"action":"model","sessionId":"from","model":"haiku"}`)))
		var result struct {
			Result surface.ControlResult `json:"result"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Result.RequestID != "request-proof" {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if unknown && (response.Code != http.StatusBadGateway || result.Result.Evidence != surface.EvidenceUnknown || result.Error.Message == "") {
			t.Fatalf("body=%s", response.Body.String())
		}
		if !unknown && response.Code != http.StatusOK {
			t.Fatalf("body=%s", response.Body.String())
		}
	}
}
