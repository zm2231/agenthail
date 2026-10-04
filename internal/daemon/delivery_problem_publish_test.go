package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestDashboardDeliveryFailurePublishesProblemToLiveCatalogImmediately(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.sendErr = surface.DeliveryTerminal(errors.New("target rejected"), surface.DeliveryInvalidRequest)
	_, events, cancel, err := d.catalog.subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"send","sessionId":"to","message":"hello"}`))
	response := httptest.NewRecorder()
	d.dashboardActionHandler(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == "delivery.problem" {
				return
			}
		case <-deadline:
			t.Fatal("delivery.problem was not published to live catalog subscribers")
		}
	}
}
