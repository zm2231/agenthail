package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

type operationSurface struct {
	*daemonSurface
	options surface.SendOptions
	queue   surface.NativeQueueRequest
	fork    surface.ForkOptions
}

func (s *operationSurface) SendWithOptions(_ context.Context, _ *surface.Session, _ string, options surface.SendOptions) (*surface.SendResult, error) {
	s.options = options
	return &surface.SendResult{UUID: "turn", Accepted: true}, nil
}
func (s *operationSurface) NativeQueue(_ context.Context, _ *surface.Session, q surface.NativeQueueRequest) (map[string]any, error) {
	s.queue = q
	return map[string]any{"data": []any{}, "nextCursor": "next"}, nil
}
func (s *operationSurface) ForkSession(_ context.Context, source *surface.Session, options surface.ForkOptions) (*surface.Session, error) {
	s.fork = options
	fork := *source
	fork.ID = "forked"
	return &fork, nil
}
func TestAdvancedOptionsWithoutModelReachDashboardAndOutbox(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"send","sessionId":"from","message":"hello","effort":"high","outputSchema":{"type":"object"}}`)))
	if w.Code != 200 || adapter.options.Effort != "high" || len(adapter.options.OutputSchema) == 0 {
		t.Fatalf("status=%d options=%+v body=%s", w.Code, adapter.options, w.Body)
	}
	id, err := r.QueueMessageWithOptions(from.ID, "queued", "", surface.SendOptions{TurnOptions: surface.TurnOptions{Effort: "xhigh", Mode: "plan"}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(state.Queue)
	if !strings.Contains(string(data), `"effort":"xhigh"`) {
		t.Fatal(string(data))
	}
	d.drainMessageQueue(context.Background(), adapter, &from)
	item, err := r.QueueItem(id, nil)
	if err != nil || item.Status != "delivered" || adapter.options.Effort != "xhigh" || adapter.options.Mode != "plan" {
		t.Fatal(item, adapter.options, err)
	}
}
func TestDashboardForkPersistsAndNativeQueuePreservesCursor(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	for _, body := range []string{`{"action":"session-fork","sessionId":"from","fork":{"lastTurnId":"turn-2","cwd":"/elsewhere"}}`, `{"action":"native-queue","sessionId":"from","nativeQueue":{"queueAction":"list","cursor":"page2"}}`} {
		w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err := r.Session("forked"); err != nil {
		t.Fatal(err)
	}
	if adapter.fork != (surface.ForkOptions{LastTurnID: "turn-2"}) || adapter.queue.Cursor != "page2" {
		t.Fatal(adapter.fork, adapter.queue)
	}
	if count := r.QueueCount("from"); count != 0 {
		t.Fatal("native queue action duplicated into Agenthail outbox", count)
	}
}

func TestACreationWithoutIdentityReportsItsOutcomeAsUnknownOrFailed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		launcher bool
		status   string
		code     string
	}{
		{name: "ambiguous", err: surface.DeliveryOutcomeUnknown(fmt.Errorf("native response lost")), status: "unknown", code: "create_unknown"},
		{name: "definite", err: fmt.Errorf("model rejected"), status: "failed", code: "create_failed"},
		{name: "ambiguous launch", err: surface.DeliveryOutcomeUnknown(fmt.Errorf("launch reply lost")), launcher: true, status: "unknown", code: "create_unknown"},
		{name: "definite launch", err: fmt.Errorf("cmux is not running"), launcher: true, status: "failed", code: "create_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, fake, _, _ := daemonFixture(t)
			d.Surfaces = []surface.Surface{&noSessionStartSurface{daemonSurface: fake, err: tc.err}}
			request := `{"action":"session-create","surface":"codex","message":"hello"}`
			if tc.launcher {
				fake.kind = surface.KindClaude
				d.Surfaces = []surface.Surface{fake}
				d.SetLaunchers([]surface.Launcher{&launcherFixture{id: surface.LauncherCMUX, agents: []surface.SurfaceKind{surface.KindClaude}, launchErr: tc.err}})
				request = fmt.Sprintf(`{"action":"session-create","surface":"claude","launcher":"cmux","message":"hello","cwd":%q}`, t.TempDir())
			}
			w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", request)
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err, w.Body.String())
			}
			if w.Code != http.StatusBadGateway || body["error"].(map[string]any)["code"] != tc.code || body["session"] != nil {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

type noSessionStartSurface struct {
	*daemonSurface
	err error
}

func (f *noSessionStartSurface) StartSession(context.Context, surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	return nil, nil, f.err
}

type unknownQueueStartSurface struct {
	*daemonSurface
	starts int
}

func (s *unknownQueueStartSurface) NativeQueue(context.Context, *surface.Session, surface.NativeQueueRequest) (map[string]any, error) {
	s.starts++
	return nil, surface.DeliveryOutcomeUnknown(errors.New("start reply lost"))
}

func TestRetriedNativeQueueStartWithSameKeyStartsOnce(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	adapter := &unknownQueueStartSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	handler := d.idempotentActionHandler(d.dashboardActionHandler)
	body := `{"action":"native-queue","sessionId":"from","nativeQueue":{"queueAction":"start"}}`
	first := serveAction(handler, actionRequestWithKey(body, "start-next"), "mac")
	retry := serveAction(handler, actionRequestWithKey(body, "start-next"), "mac")
	if adapter.starts != 1 {
		t.Fatalf("starts=%d", adapter.starts)
	}
	for _, response := range []*httptest.ResponseRecorder{first, retry} {
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), `"code":"session_operation_unknown"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}
