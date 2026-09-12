package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/zm2231/agenthail/internal/surface"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unavailableHistorySurface struct{ *timelineSurface }

func (*unavailableHistorySurface) Tail(context.Context, *surface.Session, int) ([]surface.Exchange, error) {
	return nil, errors.New("session not loaded in the agent runtime")
}
func TestLocalTimelineSurvivesMessageHistoryFailure(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &unavailableHistorySurface{&timelineSurface{daemonSurface: fake}}
	d := New(registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		Timeline          surface.SessionTimeline `json:"timeline"`
		TranscriptWarning string                  `json:"transcriptWarning"`
		Exchanges         []surface.Exchange      `json:"exchanges"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(body.Timeline.Items) != 1 || body.Timeline.UnavailableReason != "" || body.TranscriptWarning == "" || len(body.Exchanges) != 0 {
		t.Fatalf("readable local activity was lost: %s", response.Body.String())
	}
}
