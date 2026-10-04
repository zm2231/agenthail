package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

func (s *timedOutTailSurface) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{Items: []surface.TimelineItem{{ID: "tool-1", Kind: "toolCall", Title: "Read", Text: "capture"}}, Exchanges: []surface.Exchange{}, Source: "local-transcript"}, nil
}

func TestDashboardSessionMetadataEndpointRetainsProviderMetadata(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	adapter := &timedOutTailSurface{daemonSurface: fake}
	adapter.caps.Model = true
	start := time.Now()
	d := New(registry, []surface.Surface{adapter})
	request := httptest.NewRequest(http.MethodGet, "/api/session-metadata?id=from", nil)
	response := httptest.NewRecorder()
	d.dashboardSessionMetadataHandler(response, request)

	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("session metadata exceeded bounded test budget: %s", elapsed)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		SessionID string                `json:"sessionId"`
		Model     string                `json:"model"`
		Models    []surface.ModelOption `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SessionID != "from" || body.Model != "claude-opus-5[1m]" || len(body.Models) != 1 {
		t.Fatalf("metadata was lost during the activity read: %+v", body)
	}
	if body.Models[0].DefaultReasoningEffort != "high" || len(body.Models[0].SupportedReasoningEfforts) != 2 {
		t.Fatalf("model capability metadata was lost: %+v", body.Models[0])
	}
}

type metadataErrorSurface struct {
	*daemonSurface
	contextErr error
	modelErr   error
	modelsErr  error
}

func (s *metadataErrorSurface) ContextUsage(context.Context, *surface.Session) (*surface.ContextUsage, error) {
	if s.contextErr != nil {
		return nil, s.contextErr
	}
	return &surface.ContextUsage{UsedTokens: 1}, nil
}

func (s *metadataErrorSurface) Model(context.Context, *surface.Session, string) (string, error) {
	if s.modelErr != nil {
		return "", s.modelErr
	}
	return "model", nil
}

func (s *metadataErrorSurface) Models(context.Context) ([]surface.ModelOption, error) {
	if s.modelsErr != nil {
		return nil, s.modelsErr
	}
	return []surface.ModelOption{{ID: "model"}}, nil
}

func TestDashboardSessionMetadataReportsIndependentProviderErrors(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*metadataErrorSurface)
		wantError string
	}{
		{name: "context", configure: func(s *metadataErrorSurface) { s.contextErr = errors.New("context unavailable") }, wantError: "context"},
		{name: "model", configure: func(s *metadataErrorSurface) { s.modelErr = errors.New("model unavailable") }, wantError: "model"},
		{name: "models", configure: func(s *metadataErrorSurface) { s.modelsErr = errors.New("models unavailable") }, wantError: "models"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, registry, fake, _, _ := daemonFixture(t)
			fake.caps.Model = true
			adapter := &metadataErrorSurface{daemonSurface: fake}
			test.configure(adapter)
			d := New(registry, []surface.Surface{adapter})
			response := httptest.NewRecorder()
			d.dashboardSessionMetadataHandler(response, httptest.NewRequest(http.MethodGet, "/api/session-metadata?id=from", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var body struct {
				Errors map[string]string `json:"errors"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Errors[test.wantError] != "Metadata unavailable." || len(body.Errors) != 1 {
				t.Fatalf("errors=%v", body.Errors)
			}
		})
	}
}

type blockingMetadataSurface struct {
	*daemonSurface
	started chan string
	release chan struct{}
	done    chan struct{}
}

func (s *blockingMetadataSurface) block(field string) error {
	s.started <- field
	<-s.release
	s.done <- struct{}{}
	return errors.New(field + " provider ignored cancellation")
}

func (s *blockingMetadataSurface) ContextUsage(context.Context, *surface.Session) (*surface.ContextUsage, error) {
	return nil, s.block("context")
}

func (s *blockingMetadataSurface) Model(context.Context, *surface.Session, string) (string, error) {
	return "", s.block("model")
}

func (s *blockingMetadataSurface) Models(context.Context) ([]surface.ModelOption, error) {
	return nil, s.block("models")
}

func TestDashboardSessionMetadataReturnsOnRequestCancellationWhenProvidersIgnoreContext(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	fake.caps.Model = true
	adapter := &blockingMetadataSurface{daemonSurface: fake, started: make(chan string, 3), release: make(chan struct{}), done: make(chan struct{}, 3)}
	d := New(registry, []surface.Surface{adapter})
	requestContext, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	request := httptest.NewRequestWithContext(requestContext, http.MethodGet, "/api/session-metadata?id=from", nil)
	response := httptest.NewRecorder()
	started := time.Now()
	d.dashboardSessionMetadataHandler(response, request)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("metadata response waited for ignoring providers: %s", elapsed)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Errors map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Errors) != 3 || body.Errors["context"] == "" || body.Errors["model"] == "" || body.Errors["models"] == "" {
		t.Fatalf("errors=%v body=%s", body.Errors, response.Body.String())
	}
	for range 3 {
		<-adapter.started
	}
	close(adapter.release)
	for range 3 {
		select {
		case <-adapter.done:
		case <-time.After(time.Second):
			t.Fatal("provider goroutine did not unblock after response")
		}
	}
}

type nilGoalSurface struct{ *daemonSurface }

func (*nilGoalSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}

func TestDashboardSessionMetadataExplicitlyClearsNilGoal(t *testing.T) {
	_, registry, fake, _, _ := daemonFixture(t)
	fake.caps.Goal = true
	d := New(registry, []surface.Surface{&nilGoalSurface{daemonSurface: fake}})
	response := httptest.NewRecorder()
	d.dashboardSessionMetadataHandler(response, httptest.NewRequest(http.MethodGet, "/api/session-metadata?id=from", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"goal":null`) || strings.Contains(response.Body.String(), `"errors"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
