package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type timelineSurface struct {
	*daemonSurface
	cursor int64
	fail   bool
}

func (s *timelineSurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.cursor = request.Before
	if s.fail {
		return nil, errors.New("private local path")
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "tool-1", Kind: "toolCall", Title: "Bash", Text: "go test", CallID: "call-1"}}, Exchanges: []surface.Exchange{}, NextBefore: 123, Source: "local-transcript"}, nil
}
func TestAPIV1MobileTimelinePreservesContractAndReadAuthorization(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &timelineSurface{daemonSurface: fake}
	d := New(registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	for index := 1; index <= 5; index++ {
		role := "assistant"
		if index == 1 {
			role = "user"
		}
		payload := `{"itemId":"item-` + string(rune('0'+index)) + `","version":1,"op":"upsert","kind":"text","role":"` + role + `","body":"body-` + string(rune('0'+index)) + `","bodyRef":"ref-` + string(rune('0'+index)) + `"}`
		if _, _, err := registry.AppendSessionJournalEntry(registrypkg.SessionJournalEntry{SessionID: "from", Kind: "text", ProviderKey: "item-" + string(rune('0'+index)), Payload: []byte(payload), BodyRef: "ref-" + string(rune('0'+index)), FullBody: []byte("body-" + string(rune('0'+index)))}, registrypkg.SessionJournalRetention{Count: 32, Bytes: 4096}); err != nil {
			t.Fatal(err)
		}
	}
	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=4", nil)
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
		if body.Timeline.Source != "journal" || body.Timeline.NextBefore != 2 || len(body.Timeline.Items) != 4 || body.Timeline.Items[0].ID != "item-2" || body.Timeline.Items[0].Role != "assistant" || body.Timeline.Items[0].BodyRef != "ref-2" {
			t.Fatalf("%+v", body)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&timelineBefore=2&limit=4", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		Timeline surface.SessionTimeline `json:"timeline"`
	}
	json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || body.Timeline.Source != "journal" || len(body.Timeline.Items) != 1 || body.Timeline.Items[0].ID != "item-1" || body.Timeline.Items[0].Role != "user" || body.Timeline.Items[0].BodyRef != "ref-1" || strings.Contains(response.Body.String(), "private local path") {
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
