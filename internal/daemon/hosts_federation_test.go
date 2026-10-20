package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"runtime"
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
	sources  []string
}

func (f *federatedSurface) record(message string) {
	f.mu.Lock()
	f.messages = append(f.messages, message)
	f.mu.Unlock()
}

func (f *federatedSurface) senders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sources...)
}

func (f *federatedSurface) delivered() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.messages...)
}

func (f *federatedSurface) Send(ctx context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	f.mu.Lock()
	f.sources = append(f.sources, surface.SourceSessionID(ctx))
	f.mu.Unlock()
	f.record(message)
	return &surface.SendResult{UUID: "remote-turn", Accepted: true}, nil
}

func (f *federatedSurface) SendWithOptions(ctx context.Context, _ *surface.Session, message string, _ surface.SendOptions) (*surface.SendResult, error) {
	f.mu.Lock()
	f.sources = append(f.sources, surface.SourceSessionID(ctx))
	f.mu.Unlock()
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

// actionGate drops the answers to the next N actions after applying them, or
// truncates their bodies after the headers went out.
// net/http replays a request carrying an Idempotency-Key once on a reused
// connection, so losing an answer for certain takes two drops.
type actionGate struct {
	drops     atomic.Int32
	truncate  atomic.Int32
	proxyDown atomic.Bool
	older     atomic.Bool
	snapshots atomic.Pointer[snapshotHold]
	versions  atomic.Pointer[snapshotHold]
	actions   atomic.Pointer[snapshotHold]
	// beyondVersion counts requests for anything other than /api/v1/version.
	beyondVersion atomic.Int32
}

// snapshotHold parks the requests it is installed for until release is closed.
type snapshotHold struct {
	waiting atomic.Int32
	release chan struct{}
}

func (g *actionGate) take() bool {
	for {
		left := g.drops.Load()
		if left <= 0 {
			return false
		}
		if g.drops.CompareAndSwap(left, left-1) {
			return true
		}
	}
}

func allowLoopbackHosts(t *testing.T) {
	t.Helper()
	original := hosts.TailnetPrefixes
	hosts.TailnetPrefixes = append(append([]netip.Prefix(nil), original...), netip.MustParsePrefix("127.0.0.0/8"))
	t.Cleanup(func() { hosts.TailnetPrefixes = original })
}

func newFederationNode(t *testing.T, sessionIDs ...string) *federationNode {
	t.Helper()
	allowLoopbackHosts(t)
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	sessions := map[string]surface.Session{}
	cwd := t.TempDir()
	for _, id := range sessionIDs {
		session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work " + id, Cwd: cwd, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", LastActive: time.Now()}
		sessions[id] = session
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	fake := &federatedSurface{daemonSurface: &daemonSurface{sessions: sessions, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Steer: true, Interrupt: true, Stream: true, Compact: true, Model: true, Goal: true, GoalSet: true}}}
	d := New(reg, []surface.Surface{fake})
	node := &federationNode{daemon: d, reg: reg, fake: fake, gate: &actionGate{}}
	node.handler = d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
	d.discoverCatalog(context.Background())
	return node
}

func (n *federationNode) serve(t *testing.T, listener net.Listener) {
	t.Helper()
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.gate.proxyDown.Load() {
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}
		if r.URL.Path != "/api/v1/version" {
			n.gate.beyondVersion.Add(1)
		}
		if hold := n.gate.versions.Load(); hold != nil && r.URL.Path == "/api/v1/version" {
			hold.waiting.Add(1)
			<-hold.release
		}
		if hold := n.gate.actions.Load(); hold != nil && r.URL.Path == "/api/v1/actions" {
			hold.waiting.Add(1)
			<-hold.release
		}
		if n.gate.older.Load() && r.URL.Path == "/api/v1/version" {
			identity, _ := n.reg.HostIdentity()
			w.Header().Set(hosts.ResponseHeader, identity.ID)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "minimumProtocol": 1, "maximumProtocol": 1, "hostId": identity.ID, "hostName": identity.Name})
			return
		}
		if hold := n.gate.snapshots.Load(); hold != nil && r.URL.Path == "/api/v1/snapshot" {
			hold.waiting.Add(1)
			<-hold.release
		}
		if r.URL.Path == "/api/v1/actions" && n.gate.truncate.Add(-1) >= 0 {
			recorder := httptest.NewRecorder()
			n.handler.ServeHTTP(recorder, r)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("response writer cannot hijack")
				return
			}
			conn, buffered, err := hijacker.Hijack()
			if err != nil {
				return
			}
			fmt.Fprintf(buffered, "HTTP/1.1 %d OK\r\n", recorder.Code)
			_ = recorder.Header().Write(buffered)
			fmt.Fprintf(buffered, "Content-Length: %d\r\n\r\n", recorder.Body.Len()+64)
			_, _ = buffered.Write(recorder.Body.Bytes()[:recorder.Body.Len()/2])
			_ = buffered.Flush()
			conn.Close()
			return
		}
		if r.URL.Path == "/api/v1/actions" && n.gate.take() {
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
	pairing, err := n.reg.CreateDevicePairing("peer", []string{"read", "control", registry.HostScope}, time.Minute)
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
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: dashboardTestToken})
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

// awaitReplayed drains held sends until the remote applies one. A catalog
// stream that opened while the proxy was down leaves the host unreachable
// until the mirror loop reconnects, and a drain skips a host that is not
// connected.
func awaitReplayed(t *testing.T, local, remote *federationNode, host *pairedHost, message string) {
	t.Helper()
	awaitFederation(t, "held send replayed", func() bool {
		local.daemon.drainHostDeliveries(context.Background(), host)
		return len(remote.fake.delivered()) > 0
	})
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := remote.fake.delivered(); len(got) != 1 || got[0] != message {
		t.Fatalf("remote delivered=%v", got)
	}
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
	if got := remote.fake.delivered(); crossHost.Code != http.StatusOK || got[len(got)-1] != "relay" || !strings.HasPrefix(lastSender(t, remote), "unrepliable:") {
		t.Fatalf("cross-host status=%d body=%s delivered=%v from=%v", crossHost.Code, crossHost.Body.String(), got, remote.fake.senders())
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
	onHost := local.request(t, http.MethodGet, "/api/v1/resolve?scope=remote&host=forge&target=remote-b", nil)
	if err := json.Unmarshal(onHost.Body.Bytes(), &answer); err != nil || len(answer.Matches) != 1 || answer.Matches[0].ID != hosts.Qualify(record.ID, "remote-b") {
		t.Fatalf("host-scoped resolve status=%d body=%s", onHost.Code, onHost.Body.String())
	}
	if missing := local.request(t, http.MethodGet, "/api/v1/resolve?scope=remote&host=nowhere&target=remote-b", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("unknown host resolve status=%d body=%s", missing.Code, missing.Body.String())
	}

	listing := local.request(t, http.MethodGet, "/api/v1/hosts", nil)
	if listing.Code != http.StatusOK || strings.Contains(listing.Body.String(), "ahd_") || !strings.Contains(listing.Body.String(), `"sessionCount":2`) {
		t.Fatalf("hosts status=%d body=%s", listing.Code, listing.Body.String())
	}
}

func TestFederationHostRowCollidingWithLocalSessionLeavesTheLocalRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := newFederationNode(t, "remote-a", "moved")
	remote.serve(t, nil)
	local := newFederationNode(t, "moved")
	local.start(t, testRoots(remote.server))
	record := local.pair(t, remote, "forge")
	awaitFederation(t, "remote sessions mirrored", func() bool {
		return len(sessionsOf(local.snapshot(t), record.ID)) == 1
	})
	if owner, err := local.reg.SessionHost("moved"); err != nil || owner != "" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	rows, err := local.reg.HostCatalogRows(record.ID)
	if err != nil || len(rows) != 1 || rows[0].Session.ID != "remote-a" {
		t.Fatalf("mirrored rows=%+v err=%v", rows, err)
	}
	for _, session := range local.snapshot(t).Sessions {
		if session.ID == hosts.Qualify(record.ID, "moved") {
			t.Fatalf("colliding host row shown: %+v", session)
		}
		if session.ID == "moved" && (session.Host == nil || session.Host.State != hostStateLocal || session.Name != "work moved") {
			t.Fatalf("local row changed: %+v", session)
		}
	}
	for _, id := range []string{"moved", "remote-a"} {
		session := remote.fake.sessions[id]
		session.Name = "renamed " + id
		session.LastActive = time.Now()
		remote.fake.sessions[id] = session
	}
	remote.daemon.discoverCatalog(context.Background())
	awaitFederation(t, "later upserts still mirrored", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 1 && rows[0].Name == "renamed remote-a"
	})
	if session, err := local.reg.Session("moved"); err != nil || session.Name != "work moved" {
		t.Fatalf("local row after remote upsert=%+v err=%v", session, err)
	}
	want := "1 session on forge not shown: conversation moved is also a local session"
	if notice := hostNotice(t, local, record.ID); notice != want {
		t.Fatalf("hosts notice=%q, want %q", notice, want)
	}
	delete(remote.fake.sessions, "moved")
	awaitFederation(t, "notice cleared once the host stops serving the session", func() bool {
		remote.daemon.discoverCatalog(context.Background())
		hostsNotice, snapshotNotice := hostNotices(t, local, record.ID)
		return hostsNotice == "" && snapshotNotice == ""
	})
	if session, err := local.reg.Session("moved"); err != nil || session.Name != "work moved" {
		t.Fatalf("local row after host removal=%+v err=%v", session, err)
	}
}

func hostNotice(t *testing.T, node *federationNode, hostID string) string {
	t.Helper()
	hostsNotice, snapshotNotice := hostNotices(t, node, hostID)
	if snapshotNotice != hostsNotice {
		t.Fatalf("snapshot notice=%q, hosts API notice=%q", snapshotNotice, hostsNotice)
	}
	return hostsNotice
}

func hostNotices(t *testing.T, node *federationNode, hostID string) (string, string) {
	t.Helper()
	response := node.request(t, http.MethodGet, "/api/v1/hosts", nil)
	var listing struct {
		Hosts []hostView `json:"hosts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil {
		t.Fatalf("hosts status=%d body=%s", response.Code, response.Body.String())
	}
	notice := ""
	for _, host := range listing.Hosts {
		if host.ID == hostID {
			notice = host.Notice
		}
	}
	snapshot := node.request(t, http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	var state struct {
		Daemon struct {
			Hosts []dashboardHostHealth `json:"hosts"`
		} `json:"daemon"`
	}
	if err := json.Unmarshal(snapshot.Body.Bytes(), &state); err != nil {
		t.Fatalf("snapshot status=%d body=%s", snapshot.Code, snapshot.Body.String())
	}
	for _, host := range state.Daemon.Hosts {
		if host.ID == hostID {
			return notice, host.Notice
		}
	}
	t.Fatalf("snapshot hosts=%+v missing %s", state.Daemon.Hosts, hostID)
	return "", ""
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
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func mirroredQueueCount(t *testing.T, node *federationNode, id string) int {
	t.Helper()
	for _, session := range node.snapshot(t).Sessions {
		if session.ID == id {
			return session.QueueCount
		}
	}
	return -1
}

func TestFederationQueueCountsCombineHostAndHeldWork(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	if err := remote.reg.QueueMessage("remote-a", "queued on forge"); err != nil {
		t.Fatal(err)
	}
	remote.daemon.publishCatalogPresence()
	awaitFederation(t, "remote queue mirrored", func() bool { return mirroredQueueCount(t, local, qualified) == 1 })
	remote.stop()
	awaitFederation(t, "rows marked unavailable", func() bool {
		rows := sessionsOf(local.snapshot(t), record.ID)
		return len(rows) == 2 && rows[0].UnavailableReason != ""
	})
	if got := mirroredQueueCount(t, local, qualified); got != 1 {
		t.Fatalf("queue count after marking down=%d", got)
	}
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "held for forge"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"queued"`) {
		t.Fatalf("send while down status=%d body=%s", send.Code, send.Body.String())
	}
	state := local.snapshot(t)
	if got := mirroredQueueCount(t, local, qualified); got != 2 {
		t.Fatalf("queue count with held send=%d", got)
	}
	if len(state.Queue) != 1 || state.Queue[0].SessionID != qualified || state.Queue[0].HeldBy != registry.QueueHeldHostUnreachable {
		t.Fatalf("queue rows=%+v", state.Queue)
	}
	window, err := local.reg.CatalogEventsAfter(0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	latest := -1
	for _, event := range window.Events {
		if event.Type != "session.upserted" || event.EntityID != qualified {
			continue
		}
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		latest = payload.Session.QueueCount
	}
	if latest != 2 {
		t.Fatalf("latest catalog queue count=%d", latest)
	}
}

func TestFederationLostAnswerStaysUnknownAndReplaysByKey(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	remote.gate.drops.Store(2)
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
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func TestFederationTruncatedAnswerStaysUnknownAndReplaysByKey(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	remote.gate.truncate.Store(1)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "cut short"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"unknown"`) || !strings.Contains(send.Body.String(), `"status":"submitted"`) {
		t.Fatalf("truncated answer status=%d body=%s", send.Code, send.Body.String())
	}
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "inflight" {
		t.Fatalf("held queue=%+v err=%v", queue, err)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.drainHostDeliveries(context.Background(), host)
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := remote.fake.delivered(); len(got) != 1 || got[0] != "cut short" {
		t.Fatalf("remote delivered=%v", got)
	}
	queue, err = local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue after replay=%+v err=%v", queue, err)
	}
}

func TestFederationProxyAnswerWithDaemonDownHoldsSendAsUnknown(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	remote.gate.proxyDown.Store(true)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "behind the proxy"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"unknown"`) || !strings.Contains(send.Body.String(), `"status":"submitted"`) {
		t.Fatalf("proxy answer status=%d body=%s", send.Code, send.Body.String())
	}
	read := local.request(t, http.MethodGet, "/api/v1/session-metadata?id="+url.QueryEscape(qualified), nil)
	if read.Code != http.StatusServiceUnavailable || !strings.Contains(read.Body.String(), "host_unreachable") || !strings.Contains(read.Body.String(), "no Agenthail daemon answered") {
		t.Fatalf("read status=%d body=%s", read.Code, read.Body.String())
	}
	if got := remote.fake.delivered(); len(got) != 0 {
		t.Fatalf("remote delivered while down: %v", got)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.drainHostDeliveries(context.Background(), host)
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "inflight" {
		t.Fatalf("held while down queue=%+v err=%v", queue, err)
	}
	remote.gate.proxyDown.Store(false)
	awaitReplayed(t, local, remote, host, "behind the proxy")
	queue, err = local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "delivered" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func TestFederationUnconfirmedSendRetriesByReplayingItsKey(t *testing.T) {
	local, remote, record := pairedFederation(t)
	qualified := hosts.Qualify(record.ID, "remote-a")
	remote.gate.proxyDown.Store(true)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": qualified, "message": "retry me"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"unknown"`) {
		t.Fatalf("send status=%d body=%s", send.Code, send.Body.String())
	}
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
	id := queue[0].ID
	if err := local.reg.DeadLetterUnknown(id, errors.New("host forge has not confirmed the message")); err != nil {
		t.Fatal(err)
	}
	retry := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "queue-retry", "queueId": id})
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	if item, err := local.reg.QueueItem(id, nil); err != nil || item.Status != "pending" || item.HeldBy != registry.QueueHeldHostUnreachable {
		t.Fatalf("retried item=%+v err=%v", item, err)
	}
	remote.gate.proxyDown.Store(false)
	host, _ := local.daemon.pairedHost(record.ID)
	awaitReplayed(t, local, remote, host, "retry me")
	if item, err := local.reg.QueueItem(id, nil); err != nil || item.Status != "delivered" {
		t.Fatalf("replayed item=%+v err=%v", item, err)
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

func TestFederationRemoveWaitsForInFlightReconcile(t *testing.T) {
	local, remote, record := pairedFederation(t)
	hold := &snapshotHold{release: make(chan struct{})}
	remote.gate.snapshots.Store(hold)
	if err := local.daemon.attachHost(record); err != nil {
		t.Fatal(err)
	}
	awaitFederation(t, "reconcile fetching a snapshot", func() bool { return hold.waiting.Load() > 0 })
	local.daemon.catalog.mu.Lock()
	remote.gate.snapshots.Store(nil)
	close(hold.release)
	time.Sleep(100 * time.Millisecond)
	removed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		removed <- local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "forge"})
	}()
	time.Sleep(100 * time.Millisecond)
	local.daemon.catalog.mu.Unlock()
	select {
	case response := <-removed:
		if response.Code != http.StatusOK {
			t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("remove did not return")
	}
	time.Sleep(200 * time.Millisecond)
	if rows, err := local.reg.HostCatalogRows(record.ID); err != nil || len(rows) != 0 {
		t.Fatalf("rows after remove=%+v err=%v", rows, err)
	}
	if _, err := local.reg.Host(record.ID); err == nil {
		t.Fatal("host still paired after remove")
	}
	if rows := sessionsOf(local.snapshot(t), record.ID); len(rows) != 0 {
		t.Fatalf("snapshot rows after remove=%+v", rows)
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

func TestFederationLocalQualifierNeverReachesARemoteOwnedSession(t *testing.T) {
	local, _, record := pairedFederation(t)
	identity := local.daemon.hostIdentity()
	for _, ref := range []string{identity.ID + "::remote-a", identity.Name + "::remote-a"} {
		read := local.request(t, http.MethodGet, "/api/v1/session-metadata?id="+url.QueryEscape(ref), nil)
		if read.Code != http.StatusConflict || !strings.Contains(read.Body.String(), "session_owned") {
			t.Fatalf("%s read status=%d body=%s", ref, read.Code, read.Body.String())
		}
		send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": ref, "message": "wrong transport"})
		if send.Code != http.StatusConflict || !strings.Contains(send.Body.String(), "session_owned") {
			t.Fatalf("%s send status=%d body=%s", ref, send.Code, send.Body.String())
		}
	}
	scoped := local.request(t, http.MethodGet, "/api/v1/session-metadata?id=remote-a&host="+url.QueryEscape(identity.Name), nil)
	if scoped.Code != http.StatusConflict || !strings.Contains(scoped.Body.String(), "host_mismatch") {
		t.Fatalf("local host field status=%d body=%s", scoped.Code, scoped.Body.String())
	}
	if got := local.fake.delivered(); len(got) != 0 {
		t.Fatalf("local adapter reached: %v", got)
	}
	if owner, err := local.reg.SessionHost("remote-a"); err != nil || owner != record.ID {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
}

func TestFederationRenameRacesReadsAndLocalRouting(t *testing.T) {
	local, _, _ := pairedFederation(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, target := range []string{"/api/v1/version", "/api/v1/snapshot", "/api/v1/host"} {
		wg.Add(1)
		go func(target string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if response := local.request(t, http.MethodGet, target, nil); response.Code != http.StatusOK {
					t.Errorf("%s status=%d", target, response.Code)
					return
				}
			}
		}(target)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			identity := local.daemon.hostIdentity()
			response := local.request(t, http.MethodGet, "/api/v1/session-metadata?id="+url.QueryEscape(identity.ID+"::local-a"), nil)
			if response.Code != http.StatusOK {
				t.Errorf("local-qualified read status=%d body=%s", response.Code, response.Body.String())
				return
			}
		}
	}()
	for index := 0; index < 20; index++ {
		response := local.request(t, http.MethodPut, "/api/v1/host", map[string]string{"name": fmt.Sprintf("mac-%d", index)})
		if response.Code != http.StatusOK {
			t.Fatalf("rename status=%d body=%s", response.Code, response.Body.String())
		}
	}
	close(stop)
	wg.Wait()
	if name := local.daemon.hostIdentity().Name; name != "mac-19" {
		t.Fatalf("name=%q", name)
	}
}

// The launching closures are on the stack from creation, so a loop not yet scheduled still counts.
func hostLoopsRunning() int {
	buffer := make([]byte, 1<<22)
	stacks := string(buffer[:runtime.Stack(buffer, true)])
	return strings.Count(stacks, "daemon.(*Daemon).attachHostLocked.func")
}

func TestFederationOverlappingAttachesLeaveOneLoopPairAndRemoveStopsIt(t *testing.T) {
	local, _, record := pairedFederation(t)
	var wg sync.WaitGroup
	for index := 0; index < 8; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := local.daemon.attachHost(record); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := hostLoopsRunning(); got != 2 {
		t.Fatalf("host loops after overlapping attaches=%d", got)
	}
	response := local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "forge"})
	if response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if got := hostLoopsRunning(); got != 0 {
		t.Fatalf("host loops after remove=%d", got)
	}
	if _, found := local.daemon.pairedHost(record.ID); found {
		t.Fatal("removed host still routed")
	}
	if rows, err := local.reg.HostCatalogRows(record.ID); err != nil || len(rows) != 0 {
		t.Fatalf("rows after remove=%+v err=%v", rows, err)
	}
}

func TestFederationAttachAfterRemovalIsRefused(t *testing.T) {
	local, _, record := pairedFederation(t)
	response := local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "forge"})
	if response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if err := local.daemon.attachHost(record); err == nil {
		t.Fatal("attached a host that is no longer paired")
	}
	if _, found := local.daemon.pairedHost(record.ID); found {
		t.Fatal("unpaired host routed")
	}
	if got := hostLoopsRunning(); got != 0 {
		t.Fatalf("host loops for an unpaired host=%d", got)
	}
}

func TestFederationRemovalCommitsAndRevokesDespiteCatalogGap(t *testing.T) {
	local, remote, record := pairedFederation(t)
	local.daemon.catalog.mu.Lock()
	local.daemon.catalog.publishedSeq = 1 << 40
	local.daemon.catalog.mu.Unlock()
	response := local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "forge"})
	if response.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := local.reg.Host(record.ID); err == nil {
		t.Fatal("host still paired")
	}
	if _, found := local.daemon.pairedHost(record.ID); found {
		t.Fatal("removed host still routed")
	}
	if devices, err := remote.reg.ListDevices(false); err != nil || len(devices) != 0 {
		t.Fatalf("remote devices after remove=%+v err=%v", devices, err)
	}
}

func TestFederationReconcileKeepsCommittedRowsDespiteCatalogGap(t *testing.T) {
	local, remote, record := pairedFederation(t)
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.federation.lifecycle.Lock()
	local.daemon.detachHostLocked(record.ID)
	local.daemon.federation.lifecycle.Unlock()
	if err := remote.reg.RegisterSession(surface.Session{ID: "remote-c", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work remote-c", Status: surface.StatusIdle, LastActive: time.Now()}); err != nil {
		t.Fatal(err)
	}
	remote.fake.mu.Lock()
	remote.fake.sessions["remote-c"] = surface.Session{ID: "remote-c", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "work remote-c", Status: surface.StatusIdle, LastActive: time.Now()}
	remote.fake.mu.Unlock()
	remote.daemon.discoverCatalog(context.Background())
	window, err := local.reg.CatalogEventsAfter(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	local.daemon.catalog.mu.Lock()
	local.daemon.catalog.publishedSeq = window.LatestSeq + 1<<40
	local.daemon.catalog.mu.Unlock()
	if _, err := local.daemon.reconcileHost(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	rows, err := local.reg.HostCatalogRows(record.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows after gapped reconcile=%+v err=%v", rows, err)
	}
	if got := len(sessionsOf(local.snapshot(t), record.ID)); got != 3 {
		t.Fatalf("snapshot rows after gapped reconcile=%d", got)
	}
	after, err := local.reg.CatalogEventsAfter(window.EarliestSeq-1, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range after.Events {
		if event.Type == "session.removed" && strings.Contains(string(event.Payload), "omitted_by_host") {
			t.Fatalf("committed row removed: %s", event.Payload)
		}
	}
}
