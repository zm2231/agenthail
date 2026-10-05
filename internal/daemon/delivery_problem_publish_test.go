package daemon

import (
	"errors"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestDashboardDeliveryFailurePublishesProblemToLiveCatalogImmediately(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.sendErr = surface.DeliveryTerminal(errors.New("target rejected"), surface.DeliveryInvalidRequest)
	reader, closeStream := openAuthorizedCatalogStream(t, d)
	defer closeStream()
	response := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", `{"action":"send","sessionId":"to","message":"hello"}`)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if event := receiveCatalogSSE(t, reader); event.Type != "delivery.problem" {
		t.Fatalf("event=%+v", event)
	}
}
