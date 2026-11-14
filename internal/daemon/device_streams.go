package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/tailscale"
)

const (
	tailnetPathDirect  = "direct"
	tailnetPathUnknown = "unknown"

	tailnetPathsTTL      = 5 * time.Second
	tailnetStatusTimeout = 3 * time.Second
)

var errDaemonStopping = errors.New("daemon stopping")

type streamRecorder struct {
	http.ResponseWriter
	mu        sync.Mutex
	bytes     int64
	lastFlush time.Time
	err       error
}

func (s *streamRecorder) Write(p []byte) (int, error) {
	n, err := s.ResponseWriter.Write(p)
	s.mu.Lock()
	s.bytes += int64(n)
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	return n, err
}

func (s *streamRecorder) FlushError() error {
	err := http.NewResponseController(s.ResponseWriter).Flush()
	s.mu.Lock()
	if err == nil {
		s.lastFlush = time.Now()
	} else if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	return err
}

func (s *streamRecorder) Flush() {
	_ = s.FlushError()
}

func (s *streamRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func (d *Daemon) recordDeviceStream(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, _ := r.Context().Value(apiDeviceTokenContextKey{}).(string)
		deviceID, _ := r.Context().Value(actionPrincipalContextKey{}).(string)
		if token == "" || deviceID == "" || r.Method != http.MethodGet {
			next(w, r)
			return
		}
		recorder := &streamRecorder{ResponseWriter: w}
		opened := time.Now().UTC()
		next(recorder, r)
		closed := time.Now().UTC()
		recorder.mu.Lock()
		stream := registry.DeviceStream{Route: route, OpenedAt: opened, ClosedAt: closed, LastFlushAt: recorder.lastFlush.UTC(), Bytes: recorder.bytes}
		writeErr := recorder.err
		recorder.mu.Unlock()
		stream.Ended = d.streamEnded(r, writeErr)
		stream.TailnetPath = d.tailnetPaths.path(streamPeerIP(r))
		if err := d.Registry.RecordDeviceStream(deviceID, stream); err != nil {
			d.log.Printf("record %s stream close for device %s: %s", route, deviceID, err)
		}
	}
}

func (d *Daemon) streamEnded(r *http.Request, writeErr error) string {
	switch {
	case errors.Is(context.Cause(r.Context()), errDaemonStopping):
		return registry.StreamEndedDaemonStopped
	case errors.Is(writeErr, os.ErrDeadlineExceeded):
		return registry.StreamEndedWriteDeadline
	case writeErr != nil:
		return registry.StreamEndedWriteFailed
	case r.Context().Err() != nil:
		return registry.StreamEndedClientClosed
	case !d.apiEventStreamAuthorized(r):
		return registry.StreamEndedUnauthorized
	}
	return registry.StreamEndedServer
}

func streamPeerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		forwarded := r.Header.Values("X-Forwarded-For")
		if len(forwarded) == 0 {
			return ""
		}
		hops := strings.Split(forwarded[len(forwarded)-1], ",")
		ip = net.ParseIP(strings.TrimSpace(hops[len(hops)-1]))
	}
	if ip == nil {
		return ""
	}
	return ip.String()
}

var tailnetStatus = func(ctx context.Context) ([]byte, error) {
	path, err := tailscale.Executable("")
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, path, "status", "--json").Output()
}

type tailnetPathCache struct {
	mu    sync.Mutex
	read  time.Time
	paths map[string]string
}

func (c *tailnetPathCache) path(ip string) string {
	if ip == "" {
		return tailnetPathUnknown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.read.IsZero() || time.Since(c.read) >= tailnetPathsTTL {
		c.paths = readTailnetPaths()
		c.read = time.Now()
	}
	if path, ok := c.paths[ip]; ok {
		return path
	}
	return tailnetPathUnknown
}

func readTailnetPaths() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), tailnetStatusTimeout)
	defer cancel()
	output, err := tailnetStatus(ctx)
	if err != nil {
		return nil
	}
	var status struct {
		Peer map[string]struct {
			TailscaleIPs []string `json:"TailscaleIPs"`
			CurAddr      string   `json:"CurAddr"`
			Relay        string   `json:"Relay"`
		} `json:"Peer"`
	}
	if json.Unmarshal(output, &status) != nil {
		return nil
	}
	paths := map[string]string{}
	for _, peer := range status.Peer {
		path := tailnetPathUnknown
		switch {
		case peer.CurAddr != "":
			path = tailnetPathDirect
		case peer.Relay != "":
			path = "relay:" + peer.Relay
		}
		for _, address := range peer.TailscaleIPs {
			if ip := net.ParseIP(address); ip != nil {
				paths[ip.String()] = path
			}
		}
	}
	return paths
}
