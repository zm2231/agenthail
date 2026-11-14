package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

type remoteApp struct {
	name    string
	server  *httptest.Server
	port    int
	refuse  atomic.Int32
	mu      sync.Mutex
	cookies []string
}

func (a *remoteApp) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.cookies...)
}

func startRemoteApp(t *testing.T, remote *federationNode, name string) *remoteApp {
	t.Helper()
	app := &remoteApp{name: name}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "https://"+r.Host }}
	app.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.mu.Lock()
		app.cookies = append(app.cookies, r.Header.Get("Cookie"))
		app.mu.Unlock()
		session, err := r.Cookie(webSessionCookieName)
		if _, ok := remote.daemon.webSessions.find(cookieValue(session, err)); !ok || app.refuse.Load() > 0 {
			app.refuse.Add(-1)
			http.Error(w, "signed out", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: name + "-secret", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: webSessionCookieName, Value: "planted", Path: "/"})
			http.Redirect(w, r, "https://"+r.Host+"/home", http.StatusFound)
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/echo":
			body, _ := io.ReadAll(r.Body)
			_, _ = w.Write(body)
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				kind, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				_ = conn.WriteMessage(kind, append([]byte(name+":"), data...))
			}
		default:
			_, _ = io.WriteString(w, "page "+name+" "+r.Host)
		}
	}))
	app.server.StartTLS()
	t.Cleanup(app.server.Close)
	app.port = app.server.Listener.Addr().(*net.TCPAddr).Port
	return app
}

func cookieValue(cookie *http.Cookie, err error) string {
	if err != nil {
		return ""
	}
	return cookie.Value
}

type remoteBuild struct {
	body map[string]string
	key  string
}

type remoteExtensionFixture struct {
	local, remote *federationNode
	record        registry.PairedHost
	board, notes  *remoteApp
	listing       atomic.Pointer[string]
	undone        atomic.Pointer[string]
	built         atomic.Pointer[remoteBuild]
	session       webSessionView
}

func newRemoteExtensionFixture(t *testing.T) *remoteExtensionFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	previous := remoteExtensionPortBase
	remoteExtensionPortBase = 0
	t.Cleanup(func() { remoteExtensionPortBase = previous })
	f := &remoteExtensionFixture{}
	f.remote = newFederationNode(t, "remote-a")
	f.board, f.notes = startRemoteApp(t, f.remote, "board"), startRemoteApp(t, f.remote, "notes")
	f.list(fmt.Sprintf(`{"host":"running","extensions":[
		{"id":"board","label":"Board","state":"loaded","page":{"port":%d,"entry":"/","published":true}},
		{"id":"notes","label":"Notes","state":"loaded","mounts":[{"name":"app","label":"App","port":%d,"entry":"/","state":"serving","published":true}]},
		{"id":"quiet","label":"Quiet","state":"loaded","page":{"port":7999,"entry":"/"}}]}`, f.board.port, f.notes.port))
	inner := f.remote.handler
	f.remote.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/undo") && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ahd_") {
			path := r.URL.Path
			f.undone.Store(&path)
			w.Header().Set(hosts.ResponseHeader, "1")
			writeDashboardJSON(w, http.StatusOK, map[string]any{"undone": []string{}, "removed": []string{}})
			return
		}
		if r.URL.Path == "/api/v1/extensions/build" && r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ahd_") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.built.Store(&remoteBuild{body: body, key: r.Header.Get(actionIdempotencyHeader)})
			w.Header().Set(hosts.ResponseHeader, "1")
			if body["message"] == "refuse" {
				writeAPIError(w, http.StatusConflict, "extensions_off", "Extensions are off on this host.")
				return
			}
			writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "session": map[string]any{"id": "s-built", "surface": "claude"}})
			return
		}
		if r.URL.Path == "/api/v1/extensions" && r.Method == http.MethodGet && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ahd_") {
			w.Header().Set(hosts.ResponseHeader, "1")
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, *f.listing.Load())
			return
		}
		inner.ServeHTTP(w, r)
	})
	f.remote.serve(t, nil)
	f.local = newFederationNode(t, "local-a")
	f.local.start(t, testRoots(f.remote.server))
	f.record = f.local.pair(t, f.remote, "vps")
	t.Cleanup(f.local.daemon.remoteExtensions.close)
	testkit.Eventually(t, "the host is connected", 10*time.Second, func() bool { return f.local.daemon.hostConnected(f.record.ID) })
	f.session = mintWebSession(t, f.local.handler, dashboardTestToken)
	return f
}

func (f *remoteExtensionFixture) list(body string) { f.listing.Store(&body) }

func (f *remoteExtensionFixture) view(t *testing.T) extensionsView {
	t.Helper()
	response := f.local.request(t, http.MethodGet, "/api/v1/extensions?host=vps", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("the paired host's list answered %d %s", response.Code, response.Body)
	}
	var view extensionsView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func (f *remoteExtensionFixture) ports(t *testing.T) (int, int) {
	t.Helper()
	view := f.view(t)
	if len(view.Extensions) != 3 || view.Extensions[0].Page == nil || len(view.Extensions[1].Mounts) != 1 || view.Extensions[2].Page == nil {
		t.Fatalf("the list is %+v", view)
	}
	page, mount, quiet := view.Extensions[0].Page, view.Extensions[1].Mounts[0], view.Extensions[2].Page
	if page.Port == 0 || page.Port == f.board.port || page.Published || mount.Port == 0 || mount.Port == f.notes.port || mount.Published || page.Port == mount.Port {
		t.Fatalf("pages and mounts must get their own local ports: page %+v mount %+v", page, mount)
	}
	if quiet.Port != 0 || quiet.Error != "Not published on vps." {
		t.Fatalf("an unpublished page must say so: %+v", quiet)
	}
	return page.Port, mount.Port
}

func (f *remoteExtensionFixture) get(t *testing.T, port int, path string, signed bool) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if signed {
		request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: f.session.Value})
	}
	request.AddCookie(&http.Cookie{Name: "local", Value: "from-a-local-port"})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func readAll(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAPairedHostsPageOpensThroughItsOwnLocalPortSignedInByThisHost(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, mountPort := f.ports(t)

	if response := f.get(t, pagePort, "/", false); response.StatusCode != http.StatusUnauthorized || len(f.board.seen()) != 0 {
		t.Fatalf("without this host's cookie: %d, and the host saw %v", response.StatusCode, f.board.seen())
	}
	request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", pagePort), nil)
	request.Host = fmt.Sprintf("rebound.example:%d", pagePort)
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: f.session.Value})
	if response, err := http.DefaultClient.Do(request); err != nil || response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("a rebound name must be refused: %v %v", response, err)
	}

	response := f.get(t, pagePort, "/", true)
	if body := readAll(t, response); response.StatusCode != http.StatusOK || body != fmt.Sprintf("page board example.com:%d", f.board.port) {
		t.Fatalf("the page answered %d %q", response.StatusCode, body)
	}
	seen := f.board.seen()
	if strings.Contains(seen[0], f.session.Value) || strings.Contains(seen[0], "from-a-local-port") || !strings.Contains(seen[0], webSessionCookieName+"=") {
		t.Fatalf("the host must get its own session and none of this host's cookies: %q", seen[0])
	}

	response = f.get(t, mountPort, "/", true)
	if body := readAll(t, response); response.StatusCode != http.StatusOK || !strings.HasPrefix(body, "page notes") {
		t.Fatalf("the mount answered %d %q", response.StatusCode, body)
	}
}

func TestTwoPairedAppsKeepTheirOwnCookiesAndNeitherReachesTheBrowser(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, mountPort := f.ports(t)
	for _, port := range []int{pagePort, mountPort} {
		response := f.get(t, port, "/login", true)
		if response.StatusCode != http.StatusFound || len(response.Header.Values("Set-Cookie")) != 0 {
			t.Fatalf("login answered %d with cookies %v", response.StatusCode, response.Header.Values("Set-Cookie"))
		}
		if location := response.Header.Get("Location"); location != "/home" {
			t.Fatalf("a redirect to the host must stay on the local origin, got %q", location)
		}
		_ = readAll(t, f.get(t, port, "/home", true))
	}
	board, notes := f.board.seen(), f.notes.seen()
	if last := board[len(board)-1]; !strings.Contains(last, "sid=board-secret") || strings.Contains(last, "notes-secret") || strings.Contains(last, "planted") {
		t.Fatalf("board's next request carried %q", last)
	}
	if last := notes[len(notes)-1]; !strings.Contains(last, "sid=notes-secret") || strings.Contains(last, "board-secret") {
		t.Fatalf("notes' next request carried %q", last)
	}
}

func TestAPairedPageStreamsPostsAndUpgradesThroughItsPort(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, mountPort := f.ports(t)

	stream := f.get(t, pagePort, "/events", true)
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stream.Body).ReadString('\n')
		lines <- line
	}()
	select {
	case line := <-lines:
		if line != "data: first\n" {
			t.Fatalf("the stream sent %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event stream was not flushed through")
	}

	f.board.refuse.Store(1)
	request, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/echo", pagePort), strings.NewReader("hello"))
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: f.session.Value})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if body := readAll(t, response); response.StatusCode != http.StatusOK || body != "hello" {
		t.Fatalf("a post refused once must be signed in again and resent whole: %d %q", response.StatusCode, body)
	}

	header := http.Header{"Cookie": {webSessionCookieName + "=" + f.session.Value}, "Origin": {fmt.Sprintf("http://127.0.0.1:%d", mountPort)}}
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/ws", mountPort), header)
	if err != nil {
		t.Fatalf("the mount's websocket did not open: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "notes:ping" {
		t.Fatalf("the websocket answered %q %v", data, err)
	}

	if response := f.local.request(t, http.MethodDelete, "/api/v1/hosts", map[string]string{"host": "vps"}); response.Code != http.StatusOK {
		t.Fatalf("unpairing answered %d %s", response.Code, response.Body)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("unpairing must end the open websocket")
	}
}

func TestAPairedHostsPortsSurviveItBeingDownAndAreFreedWhenItIsUnpaired(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, mountPort := f.ports(t)
	path, err := remotePortsPath()
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := os.ReadFile(path)
	if !strings.Contains(string(stored), f.record.ID+"/board") || !strings.Contains(string(stored), f.record.ID+"/notes/mount/app") {
		t.Fatalf("the ports were not recorded: %s", stored)
	}

	f.local.daemon.remoteExtensions.close()
	f.local.daemon.remoteExtensions = newRemoteExtensions()
	if again, mount := f.ports(t); again != pagePort || mount != mountPort {
		t.Fatalf("after a restart the page moved from %d to %d and the mount from %d to %d", pagePort, again, mountPort, mount)
	}

	f.remote.gate.proxyDown.Store(true)
	if response := f.local.request(t, http.MethodGet, "/api/v1/extensions?host=vps", nil); response.Code == http.StatusOK {
		t.Fatalf("a host that is down answered %d", response.Code)
	}
	f.remote.gate.proxyDown.Store(false)
	testkit.Eventually(t, "the host answers again", 10*time.Second, func() bool {
		return f.local.request(t, http.MethodGet, "/api/v1/extensions?host=vps", nil).Code == http.StatusOK
	})
	if again, _ := f.ports(t); again != pagePort {
		t.Fatalf("a host that was down lost its page port: %d, was %d", again, pagePort)
	}

	f.list(`{"host":"running","extensions":[]}`)
	_ = f.view(t)
	if stored, _ := os.ReadFile(path); strings.Contains(string(stored), f.record.ID) {
		t.Fatalf("entries the host no longer lists keep ports: %s", stored)
	}
	if _, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", pagePort)); err == nil {
		t.Fatal("a dropped entry's port still listens")
	}
}

func TestAFullRemotePoolRefusesTheEntryAndKeepsTheOthers(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	remoteExtensionPortBase = free.Addr().(*net.TCPAddr).Port
	_ = free.Close()
	remoteExtensionPortCount = 1
	t.Cleanup(func() { remoteExtensionPortCount = 200 })
	view := f.view(t)
	page, mount := view.Extensions[0].Page, view.Extensions[1].Mounts[0]
	served := 0
	for _, port := range []int{page.Port, mount.Port} {
		if port != 0 {
			served++
		}
	}
	if served != 1 || (page.Port == 0 && !strings.Contains(page.Error, "no free port")) || (mount.Port == 0 && !strings.Contains(mount.Error, "no free port")) {
		t.Fatalf("with one free port exactly one entry is served and the other says why: page %+v mount %+v", page, mount)
	}
}

func TestHistoryUndoAndBuildingReachAPairedHost(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	if response := f.local.request(t, http.MethodPost, "/api/v1/extensions?host=vps", map[string]string{"action": "reload"}); response.Code != http.StatusForbidden {
		t.Fatalf("changing a paired host's extension settings answered %d %s", response.Code, response.Body)
	}
	if response := f.local.request(t, http.MethodGet, "/api/v1/extensions/history?host=vps", nil); response.Code == http.StatusNotFound && strings.Contains(response.Body.String(), "host_not_found") {
		t.Fatalf("history was not routed to the host: %s", response.Body)
	}
	if response := f.local.request(t, http.MethodPost, "/api/v1/extensions/history/abc123/undo?host=vps", nil); response.Code != http.StatusOK {
		t.Fatalf("undo on the host answered %d %s", response.Code, response.Body)
	}
	if got := f.undone.Load(); got == nil || *got != "/api/v1/extensions/history/abc123/undo" {
		t.Fatalf("the host was asked to undo %v", got)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/extensions/build?host=vps", strings.NewReader(`{"message":"a board of open pull requests","surface":"claude"}`))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(actionIdempotencyHeader, "build-once")
	response := httptest.NewRecorder()
	f.local.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("building on the paired host answered %d %s", response.Code, response.Body)
	}
	built := f.built.Load()
	if built == nil || built.body["message"] != "a board of open pull requests" || built.body["surface"] != "claude" || built.key != "build-once" {
		t.Fatalf("the host was asked to build %+v", built)
	}
	var started struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil || started.Session.ID != f.record.ID+"::s-built" {
		t.Fatalf("the builder's session came back as %q (%v): %s", started.Session.ID, err, response.Body)
	}

	if refused := f.local.request(t, http.MethodPost, "/api/v1/extensions/build?host=vps", map[string]string{"message": "refuse"}); refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "Extensions are off on this host.") {
		t.Fatalf("the host's refusal came back as %d %s", refused.Code, refused.Body)
	}
	if unknown := f.local.request(t, http.MethodPost, "/api/v1/extensions/build?host=nowhere", map[string]string{"message": "x"}); unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "host_not_found") {
		t.Fatalf("an unknown host answered %d %s", unknown.Code, unknown.Body)
	}
}

func TestAReadOnlySessionCannotPostThroughAPairedPage(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, _ := f.ports(t)
	_, token := pairWebDevice(t, f.local.reg, "read")
	reader := mintWebSession(t, f.local.handler, token)
	before := len(f.board.seen())
	request, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/echo", pagePort), strings.NewReader("x"))
	request.AddCookie(&http.Cookie{Name: webSessionCookieName, Value: reader.Value})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || len(f.board.seen()) != before {
		t.Fatalf("a read-only session posted: %d, host saw %d requests", response.StatusCode, len(f.board.seen())-before)
	}
	if got := f.get(t, pagePort, "/", false); got.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no cookie answered %d", got.StatusCode)
	}
}

func TestAPairedPageStopsReachingAHostThatFailsVerification(t *testing.T) {
	f := newRemoteExtensionFixture(t)
	pagePort, _ := f.ports(t)
	if response := f.get(t, pagePort, "/", true); response.StatusCode != http.StatusOK {
		t.Fatalf("the page answered %d before the host went away", response.StatusCode)
	}
	f.remote.gate.proxyDown.Store(true)
	host, ok := f.local.daemon.pairedHost(f.record.ID)
	if !ok {
		t.Fatal("the host is not paired")
	}
	if _, err := host.client.Version(context.Background()); err == nil {
		t.Fatal("the version probe answered while the host was down")
	}
	before := len(f.board.seen())
	if response := f.get(t, pagePort, "/", true); response.StatusCode != http.StatusServiceUnavailable || len(f.board.seen()) != before {
		t.Fatalf("an unverified host was still reached: %d, %d new requests", response.StatusCode, len(f.board.seen())-before)
	}
	f.remote.gate.proxyDown.Store(false)
	if _, err := host.client.Version(context.Background()); err != nil {
		t.Fatalf("the host did not verify again: %v", err)
	}
	if response := f.get(t, pagePort, "/", true); response.StatusCode != http.StatusOK {
		t.Fatalf("the page answered %d once the host was verified again", response.StatusCode)
	}
}

func TestAnOpenPairedWebsocketEndsWhenTheHostFailsVerification(t *testing.T) {
	previous := remoteUpgradeCheckEvery
	remoteUpgradeCheckEvery = 20 * time.Millisecond
	t.Cleanup(func() { remoteUpgradeCheckEvery = previous })
	f := newRemoteExtensionFixture(t)
	_, mountPort := f.ports(t)
	header := http.Header{"Cookie": {webSessionCookieName + "=" + f.session.Value}, "Origin": {fmt.Sprintf("http://127.0.0.1:%d", mountPort)}}
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/ws", mountPort), header)
	if err != nil {
		t.Fatalf("the mount's websocket did not open: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "notes:ping" {
		t.Fatalf("the websocket answered %q %v", data, err)
	}
	f.remote.gate.proxyDown.Store(true)
	host, ok := f.local.daemon.pairedHost(f.record.ID)
	if !ok {
		t.Fatal("the host is not paired")
	}
	if _, err := host.client.Version(context.Background()); err == nil {
		t.Fatal("the version probe answered while the host was down")
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("an open websocket kept running after the host failed verification")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the websocket was still open five seconds after verification lapsed")
	}
}
