package daemon

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/nodehost"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/tailscale"
)

type upstreamSeen struct {
	mu       sync.Mutex
	requests []*http.Request
}

func (u *upstreamSeen) record(r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, r.Clone(r.Context()))
}

func (u *upstreamSeen) last(t *testing.T) *http.Request {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.requests) == 0 {
		t.Fatal("the upstream saw no request")
	}
	return u.requests[len(u.requests)-1]
}

func (u *upstreamSeen) authorizations() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	var seen []string
	for _, r := range u.requests {
		seen = append(seen, r.Header.Get("Authorization"))
	}
	return seen
}

func startUpstream(t *testing.T) (*httptest.Server, *upstreamSeen) {
	t.Helper()
	seen := &upstreamSeen{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		switch r.URL.Path {
		case "/socket":
			conn, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
			line, _ := buffered.ReadString('\n')
			_, _ = io.WriteString(conn, "echo "+line)
			return
		case "/moved":
			http.Redirect(w, r, server.URL+"/landing?x=1", http.StatusFound)
			return
		}
		w.Header().Add("Set-Cookie", "sid=abc; Domain=example.test; Path=/; HttpOnly")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><head><title>app</title></head><body>app</body></html>")
	}))
	t.Cleanup(server.Close)
	return server, seen
}

type mountFixture struct {
	d       *Daemon
	reg     *registry.Registry
	handler http.Handler
	home    string
	dir     string
	cookie  *http.Cookie
}

func startMountFixture(t *testing.T) *mountFixture {
	t.Helper()
	d, reg, _, dashboard, handler, _ := baseFixture(t)
	dashboard.listen = "127.0.0.1:7412"
	previous := extensionMountPortBase
	extensionMountPortBase = 0
	t.Cleanup(func() { extensionMountPortBase = previous })
	home := t.TempDir()
	d.SetExtensionHost(home, func() (nodehost.Launch, error) {
		return nodehost.Launch{}, errors.New("this test runs no extension host")
	})
	t.Cleanup(d.extensions.mounts.close)
	dir := filepath.Join(extension.Workspace(home), "fixture")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"server.ts": "export default function () {}", "assets/signin.js": "window.signedIn = true;"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cookie, err := signInBrowser(reg)
	if err != nil {
		t.Fatal(err)
	}
	return &mountFixture{d: d, reg: reg, handler: handler, home: home, dir: dir, cookie: cookie}
}

func (f *mountFixture) declare(t *testing.T, upstream string) {
	t.Helper()
	manifest := fmt.Sprintf(`{"label": "Fixture", "mounts": [{"name": "app", "label": "App", "upstream": %q, "entry": "/index.html", "signin": "assets/signin.js"}]}`, upstream)
	if err := os.WriteFile(filepath.Join(f.dir, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
}

func (f *mountFixture) sync(t *testing.T) {
	t.Helper()
	if err := f.d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}
}

func (f *mountFixture) approve(t *testing.T) {
	t.Helper()
	if err := f.d.approveExtension("fixture"); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
}

func (f *mountFixture) mount(t *testing.T) mountState {
	t.Helper()
	for _, ext := range f.d.extensionsView().Extensions {
		if ext.ID == "fixture" && len(ext.Mounts) == 1 {
			return ext.Mounts[0]
		}
	}
	t.Fatal("the view lists no mount for the fixture")
	return mountState{}
}

type mountCall struct {
	path    string
	host    string
	cookie  bool
	headers map[string]string
}

func (f *mountFixture) call(t *testing.T, port int, call mountCall) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+call.path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if call.host != "" {
		request.Host = call.host
	}
	if call.cookie {
		request.AddCookie(f.cookie)
	}
	for name, value := range call.headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

func TestAMountServesItsAppOnlyToSignedInClientsAndKeepsAgenthailCredentialsFromIt(t *testing.T) {
	upstream, seen := startUpstream(t)
	f := startMountFixture(t)
	f.declare(t, upstream.URL)
	if mount := f.mount(t); mount.State != "pending" || f.d.extensions.mounts.serving("fixture/app") {
		t.Fatalf("an unapproved mount is %+v and served=%v", mount, f.d.extensions.mounts.serving("fixture/app"))
	}
	f.approve(t)
	mount := f.mount(t)
	if mount.State != "serving" || mount.Port == 0 || mount.Entry != "/index.html" {
		t.Fatalf("the approved mount is %+v", mount)
	}

	if response, _ := f.call(t, mount.Port, mountCall{path: "/"}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a request without Agenthail credentials got %d", response.StatusCode)
	}
	if response, _ := f.call(t, mount.Port, mountCall{path: "/", host: "rebound.example:80", cookie: true}); response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("a request for another host name got %d", response.StatusCode)
	}

	response, body := f.call(t, mount.Port, mountCall{path: "/", headers: map[string]string{"Cookie": f.cookie.String() + "; ah_fixture_app_sid=abc; other=1", "Origin": "http://127.0.0.1:" + strconv.Itoa(mount.Port)}})
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "app") {
		t.Fatalf("status %d body %q", response.StatusCode, body)
	}
	got := seen.last(t)
	if cookies := got.Header.Get("Cookie"); cookies != "sid=abc" {
		t.Fatalf("the upstream got cookies %q, want only its own", cookies)
	}
	if origin := got.Header.Get("Origin"); origin != upstream.URL {
		t.Fatalf("the upstream got Origin %q", origin)
	}
	if response.Header.Get("X-Frame-Options") != "" || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("the app's frame and CORS headers reached the browser: %v", response.Header)
	}
	policies := strings.Join(response.Header.Values("Content-Security-Policy"), " | ")
	if strings.Contains(policies, "'none'") || !strings.Contains(policies, "default-src 'self'") || !strings.Contains(policies, "frame-ancestors 'self' http://127.0.0.1:7412") {
		t.Fatalf("policies %q", policies)
	}
	if cookie := response.Header.Get("Set-Cookie"); !strings.HasPrefix(cookie, "ah_fixture_app_sid=abc") || strings.Contains(strings.ToLower(cookie), "domain=") {
		t.Fatalf("Set-Cookie %q", cookie)
	}

	f.call(t, mount.Port, mountCall{path: "/", headers: map[string]string{"Authorization": "Bearer secret"}})
	if got := seen.last(t); got.Header.Get("Authorization") != "" {
		t.Fatalf("the upstream got the Agenthail token %q", got.Header.Get("Authorization"))
	}
	pairing, err := f.reg.CreateDevicePairing("Phone", []string{"control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, controlOnly, err := f.reg.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	if response, _ := f.call(t, mount.Port, mountCall{path: "/", cookie: true, headers: map[string]string{"Authorization": "Bearer " + controlOnly}}); response.StatusCode != http.StatusOK {
		t.Fatalf("a signed-in GET with a control-only token got %d", response.StatusCode)
	}
	if got := seen.last(t); got.Header.Get("Authorization") != "" {
		t.Fatalf("a device token that could not read was still passed upstream: %q", got.Header.Get("Authorization"))
	}

	if response, _ := f.call(t, mount.Port, mountCall{path: "/moved", cookie: true}); response.Header.Get("Location") != "/landing?x=1" {
		t.Fatalf("Location %q", response.Header.Get("Location"))
	}

	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(mount.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET /socket HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nCookie: %s=%s\r\nOrigin: http://127.0.0.1:%d\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n", mount.Port, f.cookie.Name, f.cookie.Value, mount.Port)
	reader := bufio.NewReader(conn)
	switched, err := http.ReadResponse(reader, nil)
	if err != nil || switched.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", switched, err)
	}
	_, _ = io.WriteString(conn, "ping\n")
	if line, _ := reader.ReadString('\n'); line != "echo ping\n" {
		t.Fatalf("the upgraded connection answered %q", line)
	}
	if response, _ := f.call(t, mount.Port, mountCall{path: "/socket", cookie: true, headers: map[string]string{"Connection": "Upgrade", "Upgrade": "echo", "Origin": "http://127.0.0.1:7499"}}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("an upgrade from another origin got %d", response.StatusCode)
	}
}

func TestAMountKeyReachesOnlyTheUpstreamItWasGivenForAndOnlyFromTheAppsOwnPages(t *testing.T) {
	upstream, seen := startUpstream(t)
	f := startMountFixture(t)
	f.declare(t, upstream.URL)
	f.approve(t)
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("the-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.setExtensionMountKey("fixture", "app", &proxyacct.KeyRef{File: keyFile}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	mount := f.mount(t)
	if mount.Key != "the key file "+keyFile {
		t.Fatalf("the view describes the key as %q", mount.Key)
	}
	data, err := os.ReadFile(filepath.Join(extension.DataDir(f.home), "approvals.json"))
	if err != nil || strings.Contains(string(data), "the-real-key") {
		t.Fatalf("approvals hold the key itself: %s %v", data, err)
	}

	placeholder := "Bearer " + extensionKeyPlaceholder
	f.call(t, mount.Port, mountCall{path: "/api", cookie: true, headers: map[string]string{"Authorization": placeholder, "Sec-Fetch-Site": "same-origin"}})
	if got := seen.last(t).Header.Get("Authorization"); got != "Bearer the-real-key" {
		t.Fatalf("a same-origin request carried %q upstream", got)
	}
	f.call(t, mount.Port, mountCall{path: "/api", cookie: true, headers: map[string]string{"Authorization": placeholder, "Sec-Fetch-Site": "same-site"}})
	if got := seen.last(t).Header.Get("Authorization"); got != placeholder {
		t.Fatalf("a request from another page on this site carried %q upstream", got)
	}

	_, page := f.call(t, mount.Port, mountCall{path: "/index.html", cookie: true, headers: map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "same-site", "Referer": "http://127.0.0.1:7412/"}})
	if !strings.Contains(page, `<head><script src="/_agenthail/signin.js"></script><title>`) {
		t.Fatalf("the framed page has no sign-in script: %q", page)
	}
	_, page = f.call(t, mount.Port, mountCall{path: "/index.html", cookie: true, headers: map[string]string{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Site": "same-site", "Referer": "http://127.0.0.1:7499/"}})
	if strings.Contains(page, "signin.js") {
		t.Fatalf("a page framed by another site got the sign-in script: %q", page)
	}
	if response, script := f.call(t, mount.Port, mountCall{path: extensionSigninPath, cookie: true}); response.StatusCode != http.StatusOK || script != "window.signedIn = true;" {
		t.Fatalf("sign-in script: %d %q", response.StatusCode, script)
	}

	other, otherSeen := startUpstream(t)
	f.declare(t, other.URL)
	if moved := f.mount(t); moved.State != "pending" || f.d.extensions.mounts.serving("fixture/app") {
		t.Fatalf("a mount pointed at a new upstream is %+v, still served=%v", moved, f.d.extensions.mounts.serving("fixture/app"))
	}
	f.approve(t)
	moved := f.mount(t)
	if moved.Port != mount.Port || moved.Key != "" {
		t.Fatalf("after approving the new upstream the mount is %+v; want port %d and no key", moved, mount.Port)
	}
	f.call(t, moved.Port, mountCall{path: "/api", cookie: true, headers: map[string]string{"Authorization": placeholder, "Sec-Fetch-Site": "same-origin"}})
	if slices.Contains(otherSeen.authorizations(), "Bearer the-real-key") {
		t.Fatal("the key reached an upstream it was not given for")
	}
	if len(seen.authorizations()) == 0 {
		t.Fatal("the first upstream saw nothing")
	}
}

type fakeTailscale struct {
	mu     sync.Mutex
	routes map[int]string
}

const fakeTailnetName = "forge.tail.ts.net"

func (s *fakeTailscale) run(args ...string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case joined == "status --json":
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"` + fakeTailnetName + `.","Online":true}}`), nil
	case joined == "serve status --json":
		var tcp, web []string
		for port, target := range s.routes {
			tcp = append(tcp, fmt.Sprintf(`"%d":{"HTTPS":true}`, port))
			web = append(web, fmt.Sprintf(`"%s:%d":{"Handlers":{"/":{"Proxy":%q}}}`, fakeTailnetName, port, target))
		}
		return []byte(`{"TCP":{` + strings.Join(tcp, ",") + `},"Web":{` + strings.Join(web, ",") + `}}`), nil
	case joined == "serve status":
		var lines []string
		for port := range s.routes {
			lines = append(lines, fmt.Sprintf("https://%s:%d (tailnet only)", fakeTailnetName, port))
		}
		return []byte(strings.Join(lines, "\n")), nil
	case len(args) == 5 && args[0] == "serve" && args[1] == "--bg" && strings.HasPrefix(args[3], "--https="):
		port, _ := strconv.Atoi(strings.TrimPrefix(args[3], "--https="))
		s.routes[port] = args[4]
		return nil, nil
	case len(args) == 3 && args[0] == "serve" && args[2] == "off":
		port, _ := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(args[1], "--https="), "--http="))
		delete(s.routes, port)
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected tailscale %s", joined)
}

func (s *fakeTailscale) route(port int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routes[port]
}

func TestMountsAreOnTheTailnetOnlyWhileRemoteAccessIsOnAndOnlyTheirOwnRoutesAreRemoved(t *testing.T) {
	upstream, _ := startUpstream(t)
	f := startMountFixture(t)
	fake := &fakeTailscale{routes: map[int]string{9000: "http://127.0.0.1:9000"}}
	previous := remoteTailscale
	remoteTailscale = func(string) (tailscale.Runner, string, error) { return fake.run, "/fake/tailscale", nil }
	t.Cleanup(func() { remoteTailscale = previous })
	setRemote := func(on bool) {
		action := "remote-disable"
		if on {
			action = "remote-enable"
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/settings", strings.NewReader(`{"action": "`+action+`"}`))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		f.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, response.Code, response.Body)
		}
	}
	f.declare(t, upstream.URL)
	f.approve(t)
	port := f.mount(t).Port
	tailnetHost := fakeTailnetName + ":" + strconv.Itoa(port)
	if fake.route(port) != "" || f.mount(t).Published {
		t.Fatal("a mount was published while remote access is off")
	}
	if response, _ := f.call(t, port, mountCall{path: "/", host: tailnetHost, cookie: true}); response.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("the tailnet name was accepted while remote access is off: %d", response.StatusCode)
	}

	setRemote(true)
	if fake.route(port) != mountTarget(port) || !f.mount(t).Published {
		t.Fatalf("remote access on: route %q, view %+v", fake.route(port), f.mount(t))
	}
	if response, _ := f.call(t, port, mountCall{path: "/", host: tailnetHost, cookie: true}); response.StatusCode != http.StatusOK {
		t.Fatalf("a request through the tailnet name got %d", response.StatusCode)
	}
	_, body := f.call(t, port, mountCall{path: "/", host: tailnetHost, cookie: true})
	if !strings.Contains(body, "app") {
		t.Fatalf("body %q", body)
	}

	setRemote(false)
	if fake.route(port) != "" || f.mount(t).Published {
		t.Fatalf("remote access off left route %q", fake.route(port))
	}
	setRemote(true)
	if fake.route(port) == "" {
		t.Fatal("turning remote access back on did not publish the mount again")
	}
	if err := os.WriteFile(filepath.Join(f.dir, "extension.json"), []byte(`{"label": "Fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	if fake.route(port) != "" {
		t.Fatal("a removed mount stayed on the tailnet")
	}
	if fake.route(9000) != "http://127.0.0.1:9000" {
		t.Fatal("a route Agenthail did not publish for a mount was removed")
	}
	ports, err := extension.ReadMountPorts(f.home)
	if err != nil || len(ports) != 0 {
		t.Fatalf("a removed mount keeps its port: %v %v", ports, err)
	}
}

func TestRemoteAccessOffWithNoDaemonRunningWithdrawsTheMountsItPublished(t *testing.T) {
	upstream, _ := startUpstream(t)
	f := startMountFixture(t)
	fake := &fakeTailscale{routes: map[int]string{9000: "http://127.0.0.1:9000"}}
	previous := remoteTailscale
	remoteTailscale = func(string) (tailscale.Runner, string, error) { return fake.run, "/fake/tailscale", nil }
	t.Cleanup(func() { remoteTailscale = previous })
	config, err := LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.RemoteAccess.Enabled = true
	if err := SaveDashboardConfig(config); err != nil {
		t.Fatal(err)
	}
	f.declare(t, upstream.URL)
	f.approve(t)
	port := f.mount(t).Port
	if fake.route(port) == "" {
		t.Fatal("the mount was not published")
	}
	f.d.extensions.mounts.close()

	if err := WithdrawMountRoutes(f.home, fake.run, fakeTailnetName); err != nil {
		t.Fatal(err)
	}
	if fake.route(port) != "" {
		t.Fatal("the mount stayed on the tailnet")
	}
	if fake.route(9000) == "" {
		t.Fatal("a route Agenthail did not publish was removed")
	}
	ports, err := extension.ReadMountPorts(f.home)
	if err != nil || ports["fixture/app"].Published || ports["fixture/app"].Port != port {
		t.Fatalf("after withdrawing, mounts.json holds %v %v; want the port kept and not published", ports, err)
	}
}

func TestTheExtensionsViewCarriesWhatEachMountOffersAsAnApp(t *testing.T) {
	f := startMountFixture(t)
	manifest := `{"label": "Fixture", "app": {"label": "Board", "icon": "chart.bar"}, "mounts": [{"name": "app", "label": "App", "upstream": "http://127.0.0.1:9", "app": {"icon": "terminal", "order": 3}}]}`
	if err := os.WriteFile(filepath.Join(f.dir, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	if app := f.mount(t).App; app == nil || app.Icon != "terminal" || app.Order != 3 {
		t.Fatalf("the mount offers %+v", app)
	}
}

func TestOfferingAMountAsAnAppKeepsItsApprovalAndKey(t *testing.T) {
	upstream, _ := startUpstream(t)
	f := startMountFixture(t)
	f.declare(t, upstream.URL)
	f.approve(t)
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.setExtensionMountKey("fixture", "app", &proxyacct.KeyRef{File: keyFile}); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	before := f.mount(t)
	manifest := fmt.Sprintf(`{"label": "Fixture", "app": {"icon": "chart.bar"}, "mounts": [{"name": "app", "label": "App", "upstream": %q, "entry": "/index.html", "signin": "assets/signin.js", "app": {"label": "Console", "icon": "terminal"}}]}`, upstream.URL)
	if err := os.WriteFile(filepath.Join(f.dir, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	f.sync(t)
	after := f.mount(t)
	if after.State != "serving" || after.Port != before.Port || after.Key != before.Key || after.Key == "" {
		t.Fatalf("offering the mount as an app changed it from %+v to %+v", before, after)
	}
	if after.App == nil || after.App.Label != "Console" {
		t.Fatalf("the mount offers %+v", after.App)
	}
}
