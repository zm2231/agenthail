package surfaces

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type scriptedCall struct {
	method string
	params map[string]any
}

type scriptedThreadClient struct {
	calls     []scriptedCall
	connected []bool
	loaded    bool
	active    bool
}

func (c *scriptedThreadClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.calls = append(c.calls, scriptedCall{method: method, params: params})
	switch method {
	case codexThreadStart, codexThreadFork:
		return map[string]any{"result": map[string]any{"thread": map[string]any{"id": "new-thread"}}}, nil
	case "thread/loaded/list":
		if c.loaded {
			return map[string]any{"result": map[string]any{"data": []any{"t"}}}, nil
		}
		return map[string]any{"result": map[string]any{"data": []any{}}}, nil
	case "thread/read":
		status := "notLoaded"
		if c.active {
			status = "active"
		} else if c.loaded {
			status = "idle"
		}
		return map[string]any{"result": map[string]any{"thread": map[string]any{"status": map[string]any{"type": status}}}}, nil
	case "mcpServerStatus/list":
		connected := len(c.connected) > 0 && c.connected[0]
		if len(c.connected) > 0 {
			c.connected = c.connected[1:]
		}
		if !connected {
			return map[string]any{"result": map[string]any{"data": []any{}}}, nil
		}
		return map[string]any{"result": map[string]any{"data": []any{map[string]any{"name": mcpServerName, "runtimeStatus": "connected"}}}}, nil
	}
	return map[string]any{"result": map[string]any{}}, nil
}

func (c *scriptedThreadClient) Close() error { return nil }

func (c *scriptedThreadClient) methods() []string {
	methods := []string{}
	for _, call := range c.calls {
		methods = append(methods, call.method)
	}
	return methods
}

func bearerOf(params map[string]any) string {
	config, _ := params["config"].(map[string]any)
	servers, _ := config["mcp_servers"].(map[string]any)
	server, _ := servers[mcpServerName].(map[string]any)
	headers, _ := server["http_headers"].(map[string]any)
	value, _ := headers["Authorization"].(string)
	return value
}

type fakeCredentials struct {
	live       map[string]string
	rotations  int
	unattached []error
	claims     []string
	rotateErr  error
	expired    bool
}

func (f *fakeCredentials) Current(string) bool { return !f.expired }

func (f *fakeCredentials) URL() string { return "http://127.0.0.1:7412/mcp" }
func (f *fakeCredentials) Pending(context.Context) (string, string, error) {
	return "ahs_pending", "ref-1", nil
}
func (f *fakeCredentials) Claim(ref, id string) error {
	f.claims = append(f.claims, ref+"="+id)
	return nil
}
func (f *fakeCredentials) Live(id string) (string, bool) {
	token, ok := f.live[id]
	return token, ok
}
func (f *fakeCredentials) Rotate(id string) (string, error) {
	f.rotations++
	return "ahs_rotated", f.rotateErr
}
func (f *fakeCredentials) Unattached(_ string, err error) { f.unattached = append(f.unattached, err) }

func TestStartAndForkCarryAPendingCredential(t *testing.T) {
	for _, method := range []string{codexThreadStart, codexThreadFork} {
		credentials := &fakeCredentials{}
		c := &Codex{}
		c.SetMCPCredentials(credentials)
		client := &scriptedThreadClient{}
		_, err := c.codexThreadRequest(context.Background(), client, method, nil, map[string]any{"cwd": "/w"}, time.Second)
		if err != nil || bearerOf(client.calls[0].params) != "Bearer ahs_pending" || client.calls[0].params["cwd"] != "/w" {
			t.Fatalf("%s: err=%v, the call did not carry the pending bearer and its own params", method, err)
		}
		config, _ := client.calls[0].params["config"].(map[string]any)
		servers, _ := config["mcp_servers"].(map[string]any)
		if server, _ := servers[mcpServerName].(map[string]any); server["default_tools_approval_mode"] != "approve" {
			t.Fatalf("%s: the agenthail tools would wait for approval", method)
		}
		if len(credentials.claims) != 1 || credentials.claims[0] != "ref-1=new-thread" {
			t.Fatalf("%s: claims=%v", method, credentials.claims)
		}
	}
}

func TestResumeCarriesTheLiveTokenTheDaemonHolds(t *testing.T) {
	credentials := &fakeCredentials{live: map[string]string{"t": "ahs_live"}}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := client.methods(); len(got) != 2 || bearerOf(client.calls[1].params) != "Bearer ahs_live" {
		t.Fatalf("calls=%v", got)
	}
}

func TestALoadedThreadIsJoinedWithoutItsOverrides(t *testing.T) {
	credentials := &fakeCredentials{live: map[string]string{"t": "ahs_live"}}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	store := newMemoryCodexConfig()
	if _, err := store.SetCodexConfigOverride("t", codexCompactLimit, 100000); err != nil {
		t.Fatal(err)
	}
	c.SetCodexConfigStore(store)
	client := &scriptedThreadClient{connected: []bool{true}, loaded: true}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t", "excludeTurns": true, "cwd": "/w", "model": "m"}, time.Second); err != nil {
		t.Fatal(err)
	}
	want := []string{"thread/loaded/list", codexThreadResume, "mcpServerStatus/list"}
	if got := client.methods(); !slices.Equal(got, want) {
		t.Fatalf("calls=%v", got)
	}
	if params := client.calls[1].params; len(params) != 2 || params["threadId"] != "t" || params["excludeTurns"] != true {
		t.Fatalf("a loaded thread was resumed with %v", params)
	}
}

func TestResumeWithoutATokenLeavesAConnectedThreadAlone(t *testing.T) {
	credentials := &fakeCredentials{}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{true}}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if credentials.rotations != 0 || bearerOf(client.calls[1].params) != "" {
		t.Fatalf("rotations=%d calls=%v", credentials.rotations, client.methods())
	}
}

func TestAThreadThatUnloadedBeforeResumeIsReattachedWithARotatedToken(t *testing.T) {
	credentials := &fakeCredentials{}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{false, true}}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	want := []string{"thread/loaded/list", codexThreadResume, "mcpServerStatus/list", "thread/read", "thread/unsubscribe", codexThreadResume, "mcpServerStatus/list"}
	got := client.methods()
	if len(got) != len(want) {
		t.Fatalf("calls=%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("calls=%v", got)
		}
	}
	if credentials.rotations != 1 || bearerOf(client.calls[5].params) != "Bearer ahs_rotated" || len(credentials.unattached) != 0 {
		t.Fatalf("rotations=%d unattached=%v, the re-attach did not carry the rotated bearer", credentials.rotations, credentials.unattached)
	}
}

func TestALoadedThreadWhoseCredentialPassedItsLimitIsReloadedWithANewToken(t *testing.T) {
	credentials := &fakeCredentials{expired: true}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{true}, loaded: true}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	want := []string{"thread/loaded/list", codexThreadResume, "thread/read", "thread/turns/list", "thread/unsubscribe", codexThreadResume, "mcpServerStatus/list"}
	if got := client.methods(); !slices.Equal(got, want) {
		t.Fatalf("calls=%v", got)
	}
	if credentials.rotations != 1 || bearerOf(client.calls[5].params) != "Bearer ahs_rotated" || len(credentials.unattached) != 0 {
		t.Fatalf("rotations=%d unattached=%v, the expired token stayed attached", credentials.rotations, credentials.unattached)
	}
}

func TestAThreadWithATurnRunningIsNotReloadedForItsCredential(t *testing.T) {
	credentials := &fakeCredentials{expired: true}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{loaded: true, active: true}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	want := []string{"thread/loaded/list", codexThreadResume, "thread/read"}
	if got := client.methods(); !slices.Equal(got, want) {
		t.Fatalf("calls=%v", got)
	}
	if credentials.rotations != 0 || len(credentials.unattached) != 1 {
		t.Fatalf("rotations=%d unattached=%v", credentials.rotations, credentials.unattached)
	}
}

func TestAReattachThatCannotTakeIsReported(t *testing.T) {
	credentials := &fakeCredentials{}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{false, false}}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(credentials.unattached) != 1 || credentials.rotations != 1 {
		t.Fatalf("unattached=%v rotations=%d", credentials.unattached, credentials.rotations)
	}
}

func TestADaemonWithoutCredentialsStartsAThreadBareAndReportsItAndNoOtherProcessStartsOne(t *testing.T) {
	client := &scriptedThreadClient{}
	var unattached []string
	daemon := &Codex{}
	daemon.OpensConversations(func(id string, _ error) { unattached = append(unattached, id) })
	if _, err := daemon.codexThreadRequest(context.Background(), client, codexThreadStart, nil, map[string]any{"cwd": "/w"}, time.Second); err != nil || len(client.calls) != 1 || bearerOf(client.calls[0].params) != "" {
		t.Fatalf("err=%v calls=%v, want one start without a bearer", err, client.calls)
	}
	if len(unattached) != 1 || unattached[0] != "new-thread" {
		t.Fatalf("unattached=%v, want the new thread reported", unattached)
	}
	other := &scriptedThreadClient{}
	if _, err := (&Codex{}).codexThreadRequest(context.Background(), other, codexThreadStart, nil, map[string]any{"cwd": "/w"}, time.Second); !errors.Is(err, surface.ErrOpenIsTheDaemons) || len(other.calls) != 0 {
		t.Fatalf("err=%v calls=%v, want the start refused before Codex is asked", err, other.calls)
	}
}
