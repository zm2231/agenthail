package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/hosts"
)

var (
	remoteExtensionPortBase  = defaultRemoteExtensionPortBase
	remoteExtensionPortCount = 200
)

const (
	remoteSessionRenewBefore = time.Hour
	remoteRetryBodyLimit     = extensionActionBodyLimit
)

type remoteExtensions struct {
	mu         sync.Mutex
	ports      map[string]int
	forwarders map[string]*remoteForwarder
	sessions   map[string]remoteSession
}

type remoteSession struct {
	client    *hosts.Client
	value     string
	expiresAt time.Time
}

type remoteForwarder struct {
	key        string
	hostID     string
	port       int
	remotePort atomic.Int64
	server     *http.Server
	cancel     context.CancelFunc
	jar        *cookiejar.Jar

	mu        sync.Mutex
	client    *hosts.Client
	upstream  *url.URL
	transport *http.Transport
	upstreamP int
}

func newRemoteExtensions() *remoteExtensions {
	return &remoteExtensions{forwarders: map[string]*remoteForwarder{}, sessions: map[string]remoteSession{}}
}

func remotePortsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(extension.DataDir(home), "remote-ports.json"), nil
}

func remotePageKey(hostID, extID string) string { return hostID + "/" + extID }

func remoteMountKey(hostID, extID, mount string) string {
	return hostID + "/" + extID + "/mount/" + mount
}

func (r *remoteExtensions) loadPortsLocked() error {
	if r.ports != nil {
		return nil
	}
	path, err := remotePortsPath()
	if err != nil {
		return err
	}
	ports := map[string]int{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &ports); err != nil {
			return err
		}
	}
	r.ports = ports
	return nil
}

func (r *remoteExtensions) savePortsLocked() error {
	path, err := remotePortsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(r.ports, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600)
}

func (r *remoteExtensions) listenLocked(key string) (net.Listener, int, error) {
	if port, ok := r.ports[key]; ok {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			return nil, 0, fmt.Errorf("port %d: %w", port, err)
		}
		return listener, port, nil
	}
	if remoteExtensionPortBase == 0 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, err
		}
		return listener, listener.Addr().(*net.TCPAddr).Port, nil
	}
	taken := map[int]bool{}
	for _, port := range r.ports {
		taken[port] = true
	}
	for port := remoteExtensionPortBase; port < remoteExtensionPortBase+remoteExtensionPortCount; port++ {
		if taken[port] {
			continue
		}
		if listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
			return listener, port, nil
		}
	}
	return nil, 0, fmt.Errorf("no free port from %d to %d", remoteExtensionPortBase, remoteExtensionPortBase+remoteExtensionPortCount-1)
}

// A listed key keeps its port while unservable, so its origin survives.
func (d *Daemon) shareRemoteExtensions(host *pairedHost, view extensionsView) (extensionsView, error) {
	r := d.remoteExtensions
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadPortsLocked(); err != nil {
		return view, fmt.Errorf("read remote extension ports: %w", err)
	}
	hostID, name := host.record.ID, host.record.Name
	listed := map[string]bool{}
	changed := false
	share := func(key string, remotePort int, servable bool, failure string) (int, string) {
		listed[key] = true
		if !servable {
			if f := r.forwarders[key]; f != nil {
				f.remotePort.Store(0)
			}
			return 0, failure
		}
		f := r.forwarders[key]
		if f == nil {
			listener, port, err := r.listenLocked(key)
			if err != nil {
				return 0, err.Error()
			}
			if r.ports[key] != port {
				r.ports[key] = port
				changed = true
			}
			f = d.startRemoteForwarder(key, hostID, port, listener)
			r.forwarders[key] = f
		}
		f.remotePort.Store(int64(remotePort))
		return f.port, ""
	}
	unpublished := "Not published on " + name + "."
	for i := range view.Extensions {
		item := &view.Extensions[i]
		if item.Page != nil {
			page := *item.Page
			failure := page.Error
			if failure == "" && !page.Published {
				failure = unpublished
			}
			page.Port, page.Error = share(remotePageKey(hostID, item.ID), page.Port, page.Published && page.Port > 0 && page.Error == "", failure)
			page.Published = false
			item.Page = &page
		}
		for j := range item.Mounts {
			mount := &item.Mounts[j]
			failure := mount.Error
			if failure == "" && mount.State == "serving" && !mount.Published {
				failure = unpublished
			}
			servable := mount.State == "serving" && mount.Published && mount.Port > 0 && mount.Error == ""
			port, failure := share(remoteMountKey(hostID, item.ID, mount.Name), mount.Port, servable, failure)
			mount.Port, mount.Published, mount.Error = port, false, failure
			if failure != "" && mount.State == "serving" {
				mount.State = "failed"
			}
		}
	}
	for key := range r.ports {
		if strings.HasPrefix(key, hostID+"/") && !listed[key] {
			r.dropLocked(key)
			changed = true
		}
	}
	if changed {
		if err := r.savePortsLocked(); err != nil {
			return view, fmt.Errorf("record remote extension ports: %w", err)
		}
	}
	return view, nil
}

func (r *remoteExtensions) dropLocked(key string) {
	if f := r.forwarders[key]; f != nil {
		f.close()
		delete(r.forwarders, key)
	}
	delete(r.ports, key)
}

func (d *Daemon) forgetRemoteExtensions(hostID string) {
	r := d.remoteExtensions
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, hostID)
	if err := r.loadPortsLocked(); err != nil {
		d.log.Printf("remote extensions: %s", err)
		return
	}
	changed := false
	for key := range r.ports {
		if strings.HasPrefix(key, hostID+"/") {
			r.dropLocked(key)
			changed = true
		}
	}
	if changed {
		if err := r.savePortsLocked(); err != nil {
			d.log.Printf("remote extensions: %s", err)
		}
	}
}

func (r *remoteExtensions) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, f := range r.forwarders {
		f.close()
		delete(r.forwarders, key)
	}
}

func (f *remoteForwarder) close() {
	f.cancel()
	_ = f.server.Close()
}

func (d *Daemon) startRemoteForwarder(key, hostID string, port int, listener net.Listener) *remoteForwarder {
	jar, _ := cookiejar.New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	f := &remoteForwarder{key: key, hostID: hostID, port: port, cancel: cancel, jar: jar}
	f.server = &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           d.portFrontDoor(loopbackName, func(w http.ResponseWriter, r *http.Request, _ *dashboardServer) { d.forwardRemoteExtension(w, r, f) }),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          d.log,
	}
	go func() {
		if err := f.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.Printf("remote extension %s: port %d: %s", key, port, err)
		}
	}()
	return f
}

func loopbackName(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	switch strings.ToLower(strings.TrimSuffix(host, ".")) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (f *remoteForwarder) target(client *hosts.Client, remotePort int) (*url.URL, *http.Transport, error) {
	if err := client.Verified(); err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.client == client && f.upstreamP == remotePort && f.transport != nil {
		return f.upstream, f.transport, nil
	}
	upstream, transport, err := client.PortTarget(remotePort)
	if err != nil {
		return nil, nil, err
	}
	if f.transport != nil {
		f.transport.CloseIdleConnections()
	}
	f.client, f.upstream, f.transport, f.upstreamP = client, upstream, transport, remotePort
	return upstream, transport, nil
}

func (d *Daemon) forwardRemoteExtension(w http.ResponseWriter, r *http.Request, f *remoteForwarder) {
	host, ok := d.pairedHost(f.hostID)
	if !ok {
		writeAPIError(w, http.StatusBadGateway, "host_not_found", "That host is no longer paired.")
		return
	}
	if err := incompatibleHost(host); err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	remotePort := int(f.remotePort.Load())
	if remotePort == 0 {
		writeAPIError(w, http.StatusNotFound, "not_published", "Not published on "+host.record.Name+".")
		return
	}
	upstream, transport, err := f.target(host.client, remotePort)
	if err != nil {
		writeHostReadError(w, host, err)
		return
	}
	local := "http://" + r.Host
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstream)
			pr.Out.Host = upstream.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			if pr.Out.Header.Get("Origin") == local {
				pr.Out.Header.Set("Origin", "https://"+upstream.Host)
			}
			if referer := pr.Out.Header.Get("Referer"); strings.HasPrefix(referer, local+"/") || referer == local {
				pr.Out.Header.Set("Referer", "https://"+upstream.Host+strings.TrimPrefix(referer, local))
			}
		},
		Transport:     &remoteRoundTripper{d: d, host: host, forwarder: f, upstream: upstream, base: transport},
		FlushInterval: -1,
		ModifyResponse: func(response *http.Response) error {
			var kept []*http.Cookie
			for _, cookie := range response.Cookies() {
				if cookie.Name != webSessionCookieName {
					kept = append(kept, cookie)
				}
			}
			if len(kept) > 0 {
				f.jar.SetCookies(upstream, kept)
			}
			response.Header.Del("Set-Cookie")
			if location, err := url.Parse(response.Header.Get("Location")); err == nil && location.IsAbs() && strings.EqualFold(location.Host, upstream.Host) {
				location.Scheme, location.Host = "", ""
				response.Header.Set("Location", location.String())
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			var signin remoteSigninError
			if errors.As(err, &signin) {
				writeAPIErrorWith(w, http.StatusBadGateway, "host_signin", signin.Error(), map[string]any{"host": host.record.ID})
				return
			}
			if r.Context().Err() == nil {
				writeHostReadError(w, host, err)
			}
		},
		ErrorLog: d.log,
	}
	proxy.ServeHTTP(w, r)
}

type remoteSigninError struct {
	host string
	err  error
}

func (e remoteSigninError) Error() string {
	return fmt.Sprintf("%s did not sign this page in: %s", e.host, e.err)
}

func (e remoteSigninError) Unwrap() error { return e.err }

type remoteRoundTripper struct {
	d         *Daemon
	host      *pairedHost
	forwarder *remoteForwarder
	upstream  *url.URL
	base      http.RoundTripper
}

func (t *remoteRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	var buffered []byte
	retry := true
	if request.Body != nil && request.Body != http.NoBody {
		head, err := io.ReadAll(io.LimitReader(request.Body, remoteRetryBodyLimit+1))
		if err != nil {
			return nil, err
		}
		if len(head) > remoteRetryBodyLimit {
			retry = false
			request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), request.Body))
		} else {
			buffered = head
			request.Body = io.NopCloser(bytes.NewReader(buffered))
		}
	}
	value, err := t.d.remoteSession(request.Context(), t.host, "")
	if err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(t.signed(request, value))
	if err != nil || response.StatusCode != http.StatusUnauthorized || !retry {
		return response, err
	}
	fresh, err := t.d.remoteSession(request.Context(), t.host, value)
	if err != nil {
		return response, nil
	}
	_ = response.Body.Close()
	again := request.Clone(request.Context())
	if buffered != nil {
		again.Body = io.NopCloser(bytes.NewReader(buffered))
	}
	return t.base.RoundTrip(t.signed(again, fresh))
}

func (t *remoteRoundTripper) signed(request *http.Request, value string) *http.Request {
	out := request.Clone(request.Context())
	out.Body = request.Body
	out.Header.Del("Cookie")
	for _, cookie := range t.forwarder.jar.Cookies(t.upstream) {
		out.AddCookie(cookie)
	}
	out.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: value})
	return out
}

func (d *Daemon) remoteSession(ctx context.Context, host *pairedHost, stale string) (string, error) {
	r := d.remoteExtensions
	r.mu.Lock()
	current, ok := r.sessions[host.record.ID]
	r.mu.Unlock()
	if ok && current.client == host.client && current.value != stale && time.Until(current.expiresAt) > remoteSessionRenewBefore {
		return current.value, nil
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/web-session", nil, []byte("{}"), nil)
	if err != nil {
		return "", remoteSigninError{host: host.record.Name, err: err}
	}
	body, err := hosts.ReadResponse(response)
	if err != nil {
		return "", remoteSigninError{host: host.record.Name, err: err}
	}
	var minted webSessionView
	if err := json.Unmarshal(body, &minted); err != nil || minted.Value == "" {
		return "", remoteSigninError{host: host.record.Name, err: errors.New("the answer carried no session")}
	}
	r.mu.Lock()
	r.sessions[host.record.ID] = remoteSession{client: host.client, value: minted.Value, expiresAt: minted.ExpiresAt}
	r.mu.Unlock()
	return minted.Value, nil
}

func (d *Daemon) remoteExtensionsList(w http.ResponseWriter, r *http.Request, host *pairedHost) {
	if err := incompatibleHost(host); err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	var view extensionsView
	if err := host.client.GetJSON(r.Context(), "/api/v1/extensions", nil, &view); err != nil {
		writeHostReadError(w, host, err)
		return
	}
	shared, err := d.shareRemoteExtensions(host, view)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "remote_ports", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, shared)
}

func (d *Daemon) localExtensionsOr(local http.HandlerFunc, remote func(http.ResponseWriter, *http.Request, *pairedHost)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := strings.TrimSpace(r.URL.Query().Get("host"))
		if ref == "" || d.isLocalHostRef(ref) {
			local(w, r)
			return
		}
		host, ok := d.pairedHost(ref)
		if !ok {
			writeAPIError(w, http.StatusNotFound, "host_not_found", fmt.Sprintf("No paired host is named %q.", ref))
			return
		}
		if remote == nil || r.Method != http.MethodGet {
			writeAPIError(w, http.StatusForbidden, "host_only", "Do this on "+host.record.Name+" itself.")
			return
		}
		remote(w, r, host)
	}
}
