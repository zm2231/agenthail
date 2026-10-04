package daemon

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestConcurrentDashboardSessionAndShutdownLeavesNoAdmittedSource(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		d, _, _, from, _ := daemonFixture(t)
		server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/session?id="+from.ID+"&timeline=1", nil)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			response, requestErr := server.Client().Do(request)
			if requestErr == nil {
				response.Body.Close()
			}
		}()
		go func() {
			defer group.Done()
			<-start
			d.sources.shutdown()
		}()
		close(start)
		group.Wait()
		d.sources.shutdown()
		server.Close()

		d.sources.mu.Lock()
		admitted := len(d.sources.sources)
		d.sources.mu.Unlock()
		if admitted != 0 {
			t.Fatalf("attempt %d left admitted sources=%d", attempt, admitted)
		}
	}
}

func TestSessionSourceShutdownCancelsAndAwaitsRunningSource(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	adapter := &sourceCountingSurface{
		daemonSurface: fake,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent),
	}
	adapter.caps.Stream = true
	subscription, err := d.sources.subscribe(&from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}

	done := make(chan struct{})
	go func() {
		d.sources.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not await the running source")
	}
}
