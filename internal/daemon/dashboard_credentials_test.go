package daemon

import (
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

type heldCredentials struct {
	*daemonSurface
	entered, release chan struct{}
}

func (s *heldCredentials) SetMCPCredentials(surfaces.MCPCredentials) {
	close(s.entered)
	<-s.release
}

func TestTheDashboardAnswersNothingBeforeItsSurfacesHaveTheirCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	listener.Close()
	if err := SaveDashboardConfig(DashboardConfig{Enabled: true, Listen: listen}); err != nil {
		t.Fatal(err)
	}
	held := &heldCredentials{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}}, entered: make(chan struct{}), release: make(chan struct{})}
	d := newTestDaemon(t, r, []surface.Surface{held})
	started := make(chan *dashboardServer, 1)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(held.release) }) }
	t.Cleanup(func() {
		release()
		if dashboard := <-started; dashboard != nil {
			_ = dashboard.shutdown()
		}
	})
	go func() {
		dashboard, err := d.startDashboard()
		if err != nil {
			t.Error(err)
		}
		started <- dashboard
	}()
	<-held.entered
	answered := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + listen + "/")
		if err == nil {
			response.Body.Close()
		}
		answered <- err
	}()
	select {
	case err := <-answered:
		t.Fatalf("the dashboard answered (err=%v) while its surfaces had no MCP credentials; a session it started then would open without the agenthail MCP server", err)
	case <-time.After(2 * time.Second):
	}
	release()
	if err := <-answered; err != nil {
		t.Fatalf("the dashboard never answered once the credentials were attached: %v", err)
	}
}
