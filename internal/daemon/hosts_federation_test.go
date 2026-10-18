package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type federatedSurface struct {
	*daemonSurface
	mu       sync.Mutex
	messages []string
}

func (f *federatedSurface) record(message string) {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
}

func (f *federatedSurface) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *federatedSurface) Send(_ context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	f.record(message)
	return &surface.SendResult{UUID: "remote-turn", Accepted: true}, nil
}

func (f *federatedSurface) SendWithOptions(_ context.Context, _ *surface.Session, message string, _ surface.SendOptions) (*surface.SendResult, error) {
	f.record(message)
	return &surface.SendResult{UUID: "remote-turn", Accepted: true}, nil
}

func (f *federatedSurface) Steer(_ context.Context, _ *surface.Session, message string) error {
	f.record("steer:" + message)
	return nil
}

func (f *federatedSurface) List(context.Context) ([]surface.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]surface.Session, 0, len(f.sessions))
	for _, session := range f.sessions {
		result = append(result, session)
	}
	return result, nil
}

func (f *federatedSurface) Resolve(_ context.Context, id string) (*surface.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &session, nil
}

func (f *federatedSurface) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}

type federationNode struct {
	daemon  *Daemon
	reg     *registry.Registry
	fake    *federatedSurface
	handler http.Handler
	server  *httptest.Server
	address string
	cancel  context.CancelFunc
	gate    *actionGate
}

type actionGate struct {
	dropNext atomic.Bool
}

func newFederationNode(t *testing.T, sessionIDs ...string) *federationNode {
	t.Helper()
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	sessions := map[string]surface.Session{}
	cwd := t.TempDir()
	for _, id := range sessionIDs {
		session := surface.Session{ID: id, Surface: surface.KindCodex, Name: "work " + id, Cwd: cwd, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", LastActive: time.Now()}
		sessions[id] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	fake := &federatedSurface{daemonSurface: &daemonSurface{sessions: sessions, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Steer: true, Interrupt: true, Stream: true}}}
	d := New(reg, []surface.Surface{fake})
	node := &federationNode{daemon: d, reg: reg, fake: fake, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	d.discoverCatalog(context.Background())
	return node
}

func (n *federationNode) serve(t *testing.T, listener net.Listener) {
	t.Helper()
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/actions" && n.gate.dropNext.CompareAndSwap(true, false) {
			n.handler.ServeHTTP(httptest.NewRecorder(), r)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("response writer cannot hijack")
				return
			}
			conn, _, err := hijacker.Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		n.handler.ServeHTTP(w, r)
	})
	server := httptest.NewUnstartedServer(gated)
	if listener != nil {
		server.Listener.Close()
		server.Listener = listener
	}
	server.StartTLS()
	n.server = server
	n.address = server.Listener.Addr().(*net.TCPAddr).IP.String()
	t.Cleanup(n.stop)
}

func (n *federationNode) stop() {
	n.server.CloseClientConnections()
	n.server.Close()
}

func (n *federationNode) endpoint() string {
	return "https://example.com:" + fmt.Sprint(n.server.Listener.Addr().(*net.TCPAddr).Port)
}

func (n *federationNode) start(t *testing.T, roots *x509.CertPool) {
	t.Helper()
	n.daemon.federation.rootCAs = roots
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	n.daemon.startFederation(ctx)
	t.Cleanup(func() {
		cancel()
		n.daemon.stopFederation()
	})
}

func testRoots(server *httptest.Server) *x509.CertPool {
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

func (n *federationNode) invite(t *testing.T) string {
	t.Helper()
	pairing, err := n.reg.CreateDevicePairing("peer", []string{"read", "control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return "agenthail://pair?" + url.Values{"endpoint": []string{n.endpoint()}, "secret": []string{pairing.Secret}}.Encode()
}

func (n *federationNode) request(t *testing.T, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	n.handler.ServeHTTP(response, request)
	return response
}

func (n *federationNode) pair(t *testing.T, remote *federationNode, name string) registry.PairedHost {
	t.Helper()
	response := n.request(t, http.MethodPost, "/api/v1/hosts", map[string]string{"pairingUrl": remote.invite(t), "address": remote.address, "name": name})
	if response.Code != http.StatusCreated {
		t.Fatalf("pair status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "ahd_") {
		t.Fatalf("pairing response leaked a token: %s", response.Body.String())
	}
	record, err := n.reg.Host(name)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (n *federationNode) snapshot(t *testing.T) dashboardState {
	t.Helper()
	response := n.request(t, http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
	}
	var state dashboardState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func awaitFederation(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func sessionsOf(state dashboardState, hostID string) []dashboardSession {
	result := []dashboardSession{}
	for _, session := range state.Sessions {
		if session.Host != nil && session.Host.ID == hostID {
			result = append(result, session)
		}
	}
	return result
}

func pairedFederation(t *testing.T) (*federationNode, *federationNode, registry.PairedHost) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	remote := newFederationNode(t, "remote-a", "remote-b")
	remote.serve(t, nil)
	local := newFederationNode(t, "local-a")
	local.start(t, testRoots(remote.server))
	record := local.pair(t, remote, "forge")
	awaitFederation(t, "remote sessions mirrored", func() bool {
		return len(sessionsOf(local.snapshot(t), record.ID)) == 2
	})
	return local, remote, record
}

func TestFederationMirrorsReadsAndDrivesRemoteSessions(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remoteIdentity, _ := remote.reg.HostIdentity()
	if record.ID != remoteIdentity.ID || record.Address != remote.address {
		t.Fatalf("record=%+v remote=%+v", record, remoteIdentity)
	}
	state := local.snapshot(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	var mirrored *dashboardSession
	for index := range state.Sessions {
		session := state.Sessions[index]
		if session.ID == qualified {
			mirrored = &session
		}
		if session.ID == "local-a" && (session.Host == nil || session.Host.State != hostStateLocal) {
			t.Fatalf("local row host=%+v", session.Host)
		}
	}
	if mirrored == nil || mirrored.Host.Name != "forge" || mirrored.Host.State != hostStateConnected || mirrored.UnavailableReason != "" {
		t.Fatalf("mirrored=%+v", mirrored)
	}
	if owner, err := local.reg.SessionHost("remote-a"); err != nil || owner != record.ID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}

	metadata := local.request(t, http.MethodGet, "/api/v1/session-metadata?id="+url.QueryEscape(qualified), nil)
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), qualified) {
		t.Fatalf("metadata status=%d body=%s", metadata.Code, metadata.Body.String())
	}

	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "hello remote"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"sessionId":"`+qualified+`"`) {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	steer := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "steer", "sessionId": "forge::remote-b", "message": "turn left"})
	if steer.Code != http.StatusOK {
		t.Fatalf("steer status=%d body=%s", steer.Code, steer.Body.String())
	}
	if got := remote.fake.delivered(); len(got) != 2 || got[0] != "hello remote" || got[1] != "steer:turn left" {
		t.Fatalf("remote delivered=%v", got)
	}
	if got := local.fake.delivered(); len(got) != 0 {
		t.Fatalf("local surface received remote traffic: %v", got)
	}

	crossHost := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "sourceSessionId": "local-a", "message": "relay"})
	if crossHost.Code != http.StatusBadRequest || !strings.Contains(crossHost.Body.String(), "cannot span") {
		t.Fatalf("cross-host status=%d body=%s", crossHost.Code, crossHost.Body.String())
	}
	unknown := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": "nowhere::x", "message": "lost"})
	if unknown.Code != http.StatusBadRequest || !strings.Contains(unknown.Body.String(), "unknown host") {
		t.Fatalf("unknown host status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	resolved := local.request(t, http.MethodGet, "/api/v1/resolve?scope=remote&target=remote-a", nil)
	var answer resolveResponse
	if err := json.Unmarshal(resolved.Body.Bytes(), &answer); err != nil || len(answer.Matches) != 1 || answer.Matches[0].ID != qualified || answer.Matches[0].Host.Name != "forge" {
		t.Fatalf("resolve status=%d body=%s", resolved.Code, resolved.Body.String())
	}

	listing := local.request(t, http.MethodGet, "/api/v1/hosts", nil)
	if listing.Code != http.StatusOK || strings.Contains(listing.Body.String(), "ahd_") || !strings.Contains(listing.Body.String(), `"sessionCount":2`) {
		t.Fatalf("hosts status=%d body=%s", listing.Code, listing.Body.String())
	}
}

func TestFederationPairedBothWaysShowsEachSessionOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mac := newFederationNode(t, "mac-a")
	forge := newFederationNode(t, "forge-a", "forge-b")
	mac.serve(t, nil)
	forge.serve(t, nil)
	roots := testRoots(mac.server)
	mac.start(t, roots)
	forge.start(t, roots)
	forgeRecord := mac.pair(t, forge, "forge")
	macRecord := forge.pair(t, mac, "mac")
	awaitFederation(t, "both mirrors", func() bool {
		return len(sessionsOf(mac.snapshot(t), forgeRecord.ID)) == 2 && len(sessionsOf(forge.snapshot(t), macRecord.ID)) == 1
	})
	time.Sleep(300 * time.Millisecond)
	for name, node := range map[string]*federationNode{"mac": mac, "forge": forge} {
		seen := map[string]int{}
		for _, session := range node.snapshot(t).Sessions {
			seen[session.ID]++
			if strings.Count(session.ID, hosts.Separator) > 1 {
				t.Fatalf("%s re-exported a mirrored row: %s", name, session.ID)
			}
		}
		for id, count := range seen {
			if count != 1 {
				t.Fatalf("%s shows %s %d times", name, id, count)
			}
		}
		if len(seen) != 3 {
			t.Fatalf("%s shows %d sessions: %v", name, len(seen), seen)
		}
	}
}

func TestFederationUnreachableHostKeepsRowsAndDeliversHeldSendOnce(t *testing.T) {
	local, remote, record := pairedFederation(t)
	port := remote.server.Listener.Addr().(*net.TCPAddr).Port
	remote.stop()
	awaitFederation(t, "rows marked unavailable", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 2 && rows[0].Host.State == hostStateUnreachable && strings.Contains(rows[0].UnavailableReason, "host forge is unreachable") && rows[0].Freshness != nil && rows[0].Freshness.Stale
	})
	qualified := hosts.Qualify(record.ID, "remote-a")
	resolved := local.request(t, http.MethodGet, "/api/v1/resolve?scope=remote&target=remote-a", nil)
	if !strings.Contains(resolved.Body.String(), qualified) {
		t.Fatalf("unreachable resolve body=%s", resolved.Body.String())
	}
	read := local.request(t, http.MethodGet, "/api/v1/session-metadata?id="+url.QueryEscape(qualified), nil)
	if read.Code != http.StatusServiceUnavailable || !strings.Contains(read.Body.String(), "Host forge is unreachable") {
		t.Fatalf("read while down status=%d body=%s", read.Code, read.Body.String())
	}
	steer := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "steer", "sessionId": qualified, "message": "now"})
	if steer.Code != http.StatusServiceUnavailable {
		t.Fatalf("steer while down status=%d body=%s", steer.Code, steer.Body.String())
	}
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "held for forge"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"queued"`) {
		t.Fatalf("send while down status=%d body=%s", send.Code, send.Body.String())
	}
	if got := remote.fake.delivered(); len(got) != 0 {
		t.Fatalf("delivered while down: %v", got)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	remote.serve(t, listener)
	awaitFederation(t, "held send delivered", func() bool { return len(remote.fake.delivered()) == 1 })
	awaitFederation(t, "rows available again", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 2 && rows[0].UnavailableReason == "" && rows[0].Host.State == hostStateConnected
	})
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := remote.fake.delivered(); len(got) != 1 || got[0] != "held for forge" {
		t.Fatalf("remote delivered=%v", got)
	}
	queue, err := local.reg.ListQueue(true)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func TestFederationLostAnswerStaysUnknownAndReplaysByKey(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	remote.gate.dropNext.Store(true)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "exactly once"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"unknown"`) {
		t.Fatalf("lost answer status=%d body=%s", send.Code, send.Body.String())
	}
	if got := remote.fake.delivered(); len(got) != 1 {
		t.Fatalf("remote delivered=%v", got)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.drainHostDeliveries(context.Background(), host)
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := remote.fake.delivered(); len(got) != 1 {
		t.Fatalf("replay produced another turn: %v", got)
	}
	queue, err := local.reg.ListQueue(true)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func TestFederationRevokedTokenMarksHostUnauthorized(t *testing.T) {
	local, remote, record := pairedFederation(t)
	devices, err := remote.reg.ListDevices(false)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
	if err := remote.reg.RevokeDevice(devices[0].ID); err != nil {
		t.Fatal(err)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	host.cancel()
	if err := local.daemon.attachHost(record); err != nil {
		t.Fatal(err)
	}
	awaitFederation(t, "unauthorized host", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 2 && rows[0].Host.State == hostStateUnauthorized && strings.Contains(rows[0].UnavailableReason, "pair it again")
	})
}

func TestFederationRemoveHostDropsRowsAndRevokesDevice(t *testing.T) {
	local, remote, record := pairedFederation(t)
	response := local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "forge"})
	if response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if rows := sessionsOf(local.snapshot(t), record.ID); len(rows) != 0 {
		t.Fatalf("rows after remove=%+v", rows)
	}
	devices, err := remote.reg.ListDevices(false)
	if err != nil || len(devices) != 0 {
		t.Fatalf("remote devices after remove=%+v err=%v", devices, err)
	}
}

func TestFederationAppliesRemoteCatalogEventsIncrementally(t *testing.T) {
	local, remote, record := pairedFederation(t)
	remote.fake.mu.Lock()
	session := remote.fake.sessions["remote-a"]
	session.Status = surface.StatusBusy
	remote.fake.sessions["remote-a"] = session
	delete(remote.fake.sessions, "remote-b")
	remote.fake.mu.Unlock()
	remote.daemon.discoverCatalog(context.Background())
	remote.daemon.discoverCatalog(context.Background())
	qualified := hosts.Qualify(record.ID, "remote-a")
	awaitFederation(t, "busy status and removal mirrored", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 1 && rows[0].ID == qualified && rows[0].Status == surface.StatusBusy
	})
}

func TestFederationCreatesSessionOnRemoteHost(t *testing.T) {
	local, remote, record := pairedFederation(t)
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "session-create", "host": "forge", "surface": "codex", "message": "start remotely", "cwd": t.TempDir()})
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), hosts.Qualify(record.ID, "started")) {
		t.Fatalf("create response is not host-qualified: %s", response.Body.String())
	}
	if len(remote.fake.startOptions) != 1 || len(local.fake.startOptions) != 0 {
		t.Fatalf("remote starts=%d local starts=%d", len(remote.fake.startOptions), len(local.fake.startOptions))
	}
}

func TestFederationRelaysRemoteSessionStream(t *testing.T) {
	local, remote, record := pairedFederation(t)
	if _, _, err := remote.reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "remote-a", Kind: "text", ProviderKey: "m1", Payload: []byte(`{"itemId":"m1","version":1,"op":"upsert","kind":"text","role":"assistant","body":"from forge"}`), ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 8, Bytes: 4096}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(local.handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/session-stream?id="+url.QueryEscape(hosts.Qualify(record.ID, "remote-a")), nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("stream status=%d body=%s", response.StatusCode, body)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the remote entry: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"sessionId":"`+hosts.Qualify(record.ID, "remote-a")+`"`) || !strings.Contains(line, "from forge") {
				t.Fatalf("stream entry=%s", line)
			}
			return
		}
	}
}
