package daemon

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func stubTailnetStatus(t *testing.T, status string, err error) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	previous := tailnetStatus
	tailnetStatus = func(context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte(status), err
	}
	t.Cleanup(func() { tailnetStatus = previous })
	return calls
}

func serveDeviceStreams(t *testing.T, d *Daemon) (string, *dashboardServer) {
	t.Helper()
	serverCtx, cancel := context.WithCancelCause(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dashboard := &dashboardServer{token: dashboardTestToken, cancel: cancel}
	dashboard.server = &http.Server{Handler: d.dashboardHandler(dashboard), BaseContext: func(net.Listener) context.Context { return serverCtx }}
	go dashboard.server.Serve(listener)
	t.Cleanup(func() { dashboard.shutdown() })
	return "http://" + listener.Addr().String(), dashboard
}

func openDeviceStream(t *testing.T, url, token, forwardedFor string) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Forwarded-For", forwardedFor)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil || line != ": connected\n" {
		cancel()
		t.Fatalf("stream opened with %q: %v", line, err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return cancel
}

func waitForDeviceStreams(t *testing.T, reg *registry.Registry, deviceID, route string, want int) []registry.DeviceStream {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		streams, err := reg.DeviceStreams(deviceID, route, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(streams) >= want || time.Now().After(deadline) {
			if len(streams) != want {
				t.Fatalf("%s streams=%+v, want %d", route, streams, want)
			}
			return streams
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func phoneDevice(t *testing.T, reg *registry.Registry) (string, string) {
	t.Helper()
	token := deviceToken(t, reg, "Phone", "read")
	device, err := reg.AuthenticateDevice(token, "read")
	if err != nil {
		t.Fatal(err)
	}
	return token, device.ID
}

func TestAPhonesStreamClosesAreRecordedWithHowTheyEndedAndTheirTailnetPath(t *testing.T) {
	calls := stubTailnetStatus(t, `{"Peer":{"phone":{"TailscaleIPs":["100.88.30.99"],"Relay":"nyc"},"tablet":{"TailscaleIPs":["100.88.30.98"],"CurAddr":"192.0.2.4:41641","Relay":"nyc"}}}`, nil)
	reg := registryTestRegistry(t)
	d := newTestDaemon(t, reg, nil)
	d.log = log.New(io.Discard, "", 0)
	base, dashboard := serveDeviceStreams(t, d)
	token, deviceID := phoneDevice(t, reg)

	openDeviceStream(t, base+"/api/v1/events", token, "100.88.30.99")()
	closed := waitForDeviceStreams(t, reg, deviceID, "events", 1)[0]
	if closed.Ended != registry.StreamEndedClientClosed || closed.TailnetPath != "relay:nyc" || closed.Bytes == 0 || closed.LastFlushAt.IsZero() || closed.LastFlushAt.Before(closed.OpenedAt) || closed.ClosedAt.Before(closed.LastFlushAt) {
		t.Fatalf("client close recorded as %+v", closed)
	}

	openDeviceStream(t, base+"/api/v1/catalog-events", token, "100.88.30.98")
	openDeviceStream(t, base+"/api/v1/events", dashboardTestToken, "100.88.30.98")
	if err := dashboard.shutdown(); err != nil {
		t.Fatal(err)
	}
	stopped := waitForDeviceStreams(t, reg, deviceID, "catalog-events", 1)[0]
	if stopped.Ended != registry.StreamEndedDaemonStopped || stopped.TailnetPath != tailnetPathDirect {
		t.Fatalf("daemon stop recorded as %+v", stopped)
	}
	waitForDeviceStreams(t, reg, deviceID, "events", 1)
	if got := calls.Load(); got != 1 {
		t.Fatalf("tailscale status ran %d times for two closes a moment apart", got)
	}
}

func TestAStreamCloseRecordsAnUnknownPathWithoutTailscaleOrAMatchingPeer(t *testing.T) {
	for name, stub := range map[string]struct {
		status string
		err    error
		peer   string
	}{
		"tailscale missing": {err: errors.New("tailscale is not installed"), peer: "100.64.0.7"},
		"peer off tailnet":  {status: `{"Peer":{"other":{"TailscaleIPs":["100.64.0.7"],"CurAddr":"192.0.2.9:41641"}}}`, peer: "100.64.0.8"},
		"no forwarded peer": {status: `{"Peer":{"other":{"TailscaleIPs":["127.0.0.1"],"CurAddr":"192.0.2.9:41641"}}}`},
		"unreadable status": {status: `not json`, peer: "100.64.0.7"},
	} {
		t.Run(name, func(t *testing.T) {
			stubTailnetStatus(t, stub.status, stub.err)
			reg := registryTestRegistry(t)
			d := newTestDaemon(t, reg, nil)
			d.log = log.New(io.Discard, "", 0)
			base, _ := serveDeviceStreams(t, d)
			token, deviceID := phoneDevice(t, reg)
			openDeviceStream(t, base+"/api/v1/events", token, stub.peer)()
			if closed := waitForDeviceStreams(t, reg, deviceID, "events", 1)[0]; closed.TailnetPath != tailnetPathUnknown {
				t.Fatalf("closed=%+v", closed)
			}
		})
	}
}
