package daemon

import (
	"context"
	"encoding/json"
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
	item, err := r.QueueItem(id)
	if err != nil || item.Status != "delivered" || adapter.options.Effort != "xhigh" || adapter.options.Mode != "plan" {
		t.Fatal(item, adapter.options, err)
	}
}
func TestDashboardForkPersistsAndNativeQueuePreservesCursor(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	adapter := &operationSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{adapter}
	for _, body := range []string{`{"action":"session-fork","sessionId":"from","fork":{"beforeTurnId":"turn-2"}}`, `{"action":"native-queue","sessionId":"from","nativeQueue":{"queueAction":"list","cursor":"page2"}}`} {
		w := httptest.NewRecorder()
		d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if _, err := r.Session("forked"); err != nil {
		t.Fatal(err)
	}
	if adapter.fork.BeforeTurnID != "turn-2" || adapter.queue.Cursor != "page2" {
		t.Fatal(adapter.fork, adapter.queue)
	}
	if count := r.QueueCount("from"); count != 0 {
		t.Fatal("native queue action duplicated into Agenthail outbox", count)
	}
}

func TestDashboardUnknownCreationWithoutIdentityIsMachineReadable(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	d.Surfaces = []surface.Surface{&unknownStartSurface{daemonSurface: fake}}
	w := httptest.NewRecorder()
	d.dashboardActionHandler(w, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"session-create","surface":"codex","message":"hello"}`)))
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"unknown":true`) || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

type unknownStartSurface struct{ *daemonSurface }

func (*unknownStartSurface) StartSession(context.Context, surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	return nil, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("native response lost"))
}
