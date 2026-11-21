package codexsurface

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
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
		return map[string]any{"result": map[string]any{"data": []any{map[string]any{"name": surface.MCPServerName, "runtimeStatus": "connected"}}}}, nil
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
	server, _ := servers[surface.MCPServerName].(map[string]any)
	headers, _ := server["http_headers"].(map[string]any)
	value, _ := headers["Authorization"].(string)
	return value
}

func TestStartAndForkCarryAPendingCredential(t *testing.T) {
	for _, method := range []string{codexThreadStart, codexThreadFork} {
		credentials := &surfacetest.FakeCredentials{}
		c := &Codex{}
		c.SetMCPCredentials(credentials)
		client := &scriptedThreadClient{}
		_, err := c.codexThreadRequest(context.Background(), client, method, nil, map[string]any{"cwd": "/w"}, time.Second)
		if err != nil || bearerOf(client.calls[0].params) != "Bearer ahs_pending" || client.calls[0].params["cwd"] != "/w" {
			t.Fatalf("%s: err=%v, the call did not carry the pending bearer and its own params", method, err)
		}
		config, _ := client.calls[0].params["config"].(map[string]any)
		servers, _ := config["mcp_servers"].(map[string]any)
		if server, _ := servers[surface.MCPServerName].(map[string]any); server["default_tools_approval_mode"] != "approve" {
			t.Fatalf("%s: the agenthail tools would wait for approval", method)
		}
		if len(credentials.Claims) != 1 || credentials.Claims[0] != "ref-1=new-thread" {
			t.Fatalf("%s: claims=%v", method, credentials.Claims)
		}
	}
}

func TestResumeCarriesTheLiveTokenTheDaemonHolds(t *testing.T) {
	credentials := &surfacetest.FakeCredentials{LiveTokens: map[string]string{"t": "ahs_live"}}
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
	credentials := &surfacetest.FakeCredentials{LiveTokens: map[string]string{"t": "ahs_live"}}
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
	credentials := &surfacetest.FakeCredentials{}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{true}}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if credentials.Rotations != 0 || bearerOf(client.calls[1].params) != "" {
		t.Fatalf("rotations=%d calls=%v", credentials.Rotations, client.methods())
	}
}

func TestAThreadThatUnloadedBeforeResumeIsReattachedWithARotatedToken(t *testing.T) {
	credentials := &surfacetest.FakeCredentials{}
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
	if credentials.Rotations != 1 || bearerOf(client.calls[5].params) != "Bearer ahs_rotated" || len(credentials.UnattachedErrors) != 0 {
		t.Fatalf("rotations=%d unattached=%v, the re-attach did not carry the rotated bearer", credentials.Rotations, credentials.UnattachedErrors)
	}
}

func TestALoadedThreadWhoseCredentialPassedItsLimitIsReloadedWithANewToken(t *testing.T) {
	credentials := &surfacetest.FakeCredentials{Expired: true}
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
	if credentials.Rotations != 1 || bearerOf(client.calls[5].params) != "Bearer ahs_rotated" || len(credentials.UnattachedErrors) != 0 {
		t.Fatalf("rotations=%d unattached=%v, the expired token stayed attached", credentials.Rotations, credentials.UnattachedErrors)
	}
}

func TestAThreadWithATurnRunningIsNotReloadedForItsCredential(t *testing.T) {
	credentials := &surfacetest.FakeCredentials{Expired: true}
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
	if credentials.Rotations != 0 || len(credentials.UnattachedErrors) != 1 {
		t.Fatalf("rotations=%d unattached=%v", credentials.Rotations, credentials.UnattachedErrors)
	}
}

func TestAReattachThatCannotTakeIsReported(t *testing.T) {
	credentials := &surfacetest.FakeCredentials{}
	c := &Codex{}
	c.SetMCPCredentials(credentials)
	client := &scriptedThreadClient{connected: []bool{false, false}}
	if _, err := c.codexThreadRequest(context.Background(), client, codexThreadResume, &surface.Session{ID: "t"}, map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(credentials.UnattachedErrors) != 1 || credentials.Rotations != 1 {
		t.Fatalf("unattached=%v rotations=%d", credentials.UnattachedErrors, credentials.Rotations)
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
