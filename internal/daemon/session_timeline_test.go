package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type timelineSurface struct {
	*daemonSurface
	cursor int64
	fail   bool
}

func (s *timelineSurface) Timeline(_ context.Context, _ *surface.Session, before int64) (*surface.SessionTimeline, error) {
	s.cursor = before
	if s.fail {
		return nil, errors.New("private local path")
	}
	return &surface.SessionTimeline{Items: []surface.TimelineItem{{ID: "tool-1", Kind: "toolCall", Title: "Bash", Text: "go test", CallID: "call-1"}}, NextBefore: 123, Source: "claude"}, nil
}
func TestAPIV1MobileTimelinePreservesContractAndReadAuthorization(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &timelineSurface{daemonSurface: fake}
	d := New(registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&timelineBefore=456", nil)
		if authenticated {
			request.Header.Set("Authorization", "Bearer secret")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !authenticated {
			if response.Code != http.StatusUnauthorized {
				t.Fatal(response.Code)
			}
			continue
		}
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if adapter.cursor != 456 || body.Timeline.NextBefore != 123 || len(body.Timeline.Items) != 1 || body.Timeline.Items[0].CallID != "call-1" {
			t.Fatalf("%+v", body)
		}
	}
	adapter.fail = true
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		Timeline surface.SessionTimeline `json:"timeline"`
	}
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body.Timeline.UnavailableReason == "" || len(body.Timeline.Items) != 0 || strings.Contains(response.Body.String(), "private local path") {
		t.Fatal(response.Body.String())
	}
}
func TestAPIV1SearchUsesGuardAndExistingHistorySearch(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/search?surface=codex&q=missing", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal(response.Code)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var body map[string]any
	json.Unmarshal(response.Body.Bytes(), &body)
	if _, ok := body["results"]; !ok {
		t.Fatal(body)
	}
}
