package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

type providerHistorySurface struct {
	*daemonSurface
	mu      sync.Mutex
	befores []int64
	fail    bool
}

func (s *providerHistorySurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.befores = append(s.befores, request.Before)
	if request.Before == 0 {
		return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "recent-1", Kind: "message", Role: "user", Text: "recent ask"}, {ID: "recent-2", Kind: "message", Role: "assistant", Text: "recent answer"}}, NextBefore: 7, Source: "local-transcript"}, nil
	}
	if s.fail {
		return nil, errors.New("transcript unreadable")
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "older-1", Kind: "message", Role: "user", Text: "older ask"}}, NextBefore: 0, Source: "local-transcript"}, nil
}

func TestSessionPageContinuesIntoProviderHistoryPastSeed(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &providerHistorySurface{daemonSurface: fake}
	d := New(registry, []surface.Surface{adapter})
	defer d.sources.shutdown()
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	read := func(query string) surface.SessionTimeline {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/session?id=from&timeline=1&limit=40"+query, nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
		var body struct {
			Timeline surface.SessionTimeline `json:"timeline"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Timeline
	}
	first := read("")
	if first.Source != "journal" || len(first.Items) != 2 || first.NextBefore != encodeProviderHistoryCursor(7) {
		t.Fatalf("seeded page lost the provider boundary: %+v", first)
	}
	older := read(fmt.Sprintf("&timelineBefore=%d", first.NextBefore))
	if len(older.Items) != 1 || older.Items[0].Text != "older ask" || older.NextBefore != 0 || older.UnavailableReason != "" {
		t.Fatalf("older provider page=%+v", older)
	}
	adapter.mu.Lock()
	befores := append([]int64(nil), adapter.befores...)
	adapter.fail = true
	adapter.mu.Unlock()
	if len(befores) < 2 || befores[0] != 0 || befores[len(befores)-1] != 7 {
		t.Fatalf("provider reads=%v", befores)
	}
	failed := read(fmt.Sprintf("&timelineBefore=%d", first.NextBefore))
	if len(failed.Items) != 0 || failed.NextBefore != 0 || !strings.Contains(failed.UnavailableReason, "Older activity could not be read") {
		t.Fatalf("failed older read reported exhaustion: %+v", failed)
	}
}
