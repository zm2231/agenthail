package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type timedOutTailSurface struct{ *daemonSurface }

func (s *timedOutTailSurface) Tail(ctx context.Context, _ *surface.Session, _ int) ([]surface.Exchange, error) {
	<-ctx.Done()
	return nil, errors.New("tail timeout")
}

func (s *timedOutTailSurface) Model(ctx context.Context, _ *surface.Session, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "claude-opus-5[1m]", nil
}

func (s *timedOutTailSurface) Models(ctx context.Context) ([]surface.ModelOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []surface.ModelOption{{ID: "claude-opus-5[1m]", DisplayName: "Opus 5", SupportedReasoningEfforts: []string{"low", "high"}, DefaultReasoningEffort: "high"}}, nil
}

func (s *timedOutTailSurface) Timeline(context.Context, *surface.Session, int64) (*surface.SessionTimeline, error) {
	return &surface.SessionTimeline{Items: []surface.TimelineItem{{ID: "tool-1", Kind: "toolCall", Title: "Read", Text: "capture"}}}, nil
}

func TestDashboardSessionRetainsMetadataAfterTailTimeout(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &timedOutTailSurface{daemonSurface: fake}
	adapter.caps.Model = true
	start := time.Now()
	d := New(registry, []surface.Surface{adapter})
	request := httptest.NewRequest(http.MethodGet, "/api/session?id=from&timeline=1", nil)
	response := httptest.NewRecorder()
	d.dashboardSessionHandlerWithTimeout(response, request, 5*time.Millisecond)

	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("session metadata exceeded bounded test budget: %s", elapsed)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		TranscriptWarning string                  `json:"transcriptWarning"`
		Timeline          surface.SessionTimeline `json:"timeline"`
		Model             string                  `json:"model"`
		Models            []surface.ModelOption   `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TranscriptWarning == "" || len(body.Timeline.Items) != 1 || body.Model != "claude-opus-5[1m]" || len(body.Models) != 1 {
		t.Fatalf("metadata was lost after Tail timeout: %+v", body)
	}
	if body.Models[0].DefaultReasoningEffort != "high" || len(body.Models[0].SupportedReasoningEfforts) != 2 {
		t.Fatalf("model capability metadata was lost: %+v", body.Models[0])
	}
}

type slowSessionReads struct {
	*timedOutTailSurface
	deadlines chan time.Time
}

func (s *slowSessionReads) awaitDeadline(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	s.deadlines <- deadline
	<-ctx.Done()
	return ctx.Err()
}

func (s *slowSessionReads) Tail(ctx context.Context, _ *surface.Session, _ int) ([]surface.Exchange, error) {
	return nil, s.awaitDeadline(ctx)
}

func (s *slowSessionReads) Timeline(ctx context.Context, _ *surface.Session, _ int64) (*surface.SessionTimeline, error) {
	return nil, s.awaitDeadline(ctx)
}

func (s *slowSessionReads) Models(ctx context.Context) ([]surface.ModelOption, error) {
	return nil, s.awaitDeadline(ctx)
}

func TestDashboardSessionReadsShareOneDeadline(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	fake.caps.Model = true
	adapter := &slowSessionReads{timedOutTailSurface: &timedOutTailSurface{daemonSurface: fake}, deadlines: make(chan time.Time, 3)}
	d := New(registry, []surface.Surface{adapter})
	response := httptest.NewRecorder()
	d.dashboardSessionHandlerWithTimeout(response, httptest.NewRequest(http.MethodGet, "/api/session?id=from&timeline=1", nil), 20*time.Millisecond)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(adapter.deadlines) != 3 {
		t.Fatalf("expected transcript, activity and model reads; got %d", len(adapter.deadlines))
	}
	deadline := <-adapter.deadlines
	for range 2 {
		if actual := <-adapter.deadlines; !actual.Equal(deadline) {
			t.Fatalf("session read extended request budget from %s to %s", deadline, actual)
		}
	}
}
