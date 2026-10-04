package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionSourceManagerRejectsAdmissionAfterShutdown(t *testing.T) {
	d, _, fake, from, _ := daemonFixture(t)
	manager := d.sources
	manager.shutdown()

	if _, _, err := manager.holdSource(&from, fake, "closed-probe"); !errors.Is(err, ErrSessionSourceManagerClosed) {
		t.Fatalf("holdSource error=%v, want closed manager", err)
	}
	if _, err := manager.subscribeContext(context.Background(), &from, fake); !errors.Is(err, ErrSessionSourceManagerClosed) {
		t.Fatalf("subscribeContext error=%v, want closed manager", err)
	}
	if err := manager.seed(context.Background(), &from, fake); !errors.Is(err, ErrSessionSourceManagerClosed) {
		t.Fatalf("seed error=%v, want closed manager", err)
	}
	if _, err := manager.readHistory(context.Background(), &from, fake, 7, 20); !errors.Is(err, ErrSessionSourceManagerClosed) {
		t.Fatalf("readHistory error=%v, want closed manager", err)
	}
	if _, err := manager.prepareStream(context.Background(), &from, fake); !errors.Is(err, ErrSessionSourceManagerClosed) {
		t.Fatalf("prepareStream error=%v, want closed manager", err)
	}
	manager.refresh(&from, fake)

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.sources) != 0 {
		t.Fatalf("closed manager admitted sources=%d", len(manager.sources))
	}
}

func TestDashboardSessionDoesNotAdmitSourceAfterShutdown(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	d.sources.shutdown()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/session?id="+from.ID+"&timeline=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	d.sources.mu.Lock()
	defer d.sources.mu.Unlock()
	if len(d.sources.sources) != 0 {
		t.Fatalf("HTTP request admitted sources after shutdown=%d", len(d.sources.sources))
	}
}

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
