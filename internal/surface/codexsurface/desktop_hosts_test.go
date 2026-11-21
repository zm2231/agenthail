package codexsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
	"github.com/zm2231/agenthail/internal/testnode"
	"github.com/zm2231/agenthail/internal/voice"
)

func TestCodexDesktopBridgeFollowsTheAppServerConnection(t *testing.T) {
	node := testnode.Path(t)
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "hosts": codexHostsJS})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8')),listeners=[],sent=[];
const context=vm.createContext({window:{addEventListener:(name,f)=>listeners.push(f)},electronBridge:{sendMessageFromView:m=>{sent.push(m)}},setTimeout,clearTimeout});
assert.equal(vm.runInContext(programs.hook,context),'hooked');
// A renderer hooked after Desktop connected asks for the current state.
assert.deepEqual(sent.map(m=>m.type),['request-app-server-state']);
const emit=data=>listeners[0]({data});
const local=()=>context.__agenthailCodexDesktopRendererV2.hosts.local;
const bridge=context.__agenthailCodexDesktopRendererV2,tick=()=>new Promise(resolve=>setTimeout(resolve,0));
(async()=>{
  // Desktop reports stderr warnings as an error on a connected host; it still serves requests.
  emit({type:'codex-app-server-connection-changed',hostId:'local',state:'connected',error:{code:'connection-failed',message:'warning'},transport:'stdio',appServerVersion:'0.171.0-alpha.13',isSnapshot:true});
  emit({type:'codex-app-server-connection-changed',hostId:'durable',state:'connected',transport:'websocket',appServerVersion:'0.1.0'});
  assert.deepEqual([local().state,local().appServerVersion,local().transport,local().fatal??null],['connected','0.171.0-alpha.13','stdio',null]);
  const pending=bridge.request('thread/loaded/list',{},60000);
  await tick();
  assert.equal(sent.at(-1).type,'mcp-request');
  emit({type:'codex-app-server-fatal-error',hostId:'durable',errorMessage:'cloud host lost'});
  emit({type:'codex-app-server-fatal-error',hostId:'local',errorMessage:'app-server exited with code 1'});
  const started=Date.now(),failed=await pending;
  assert.ok(Date.now()-started<1000);
  assert.equal(failed.error.code,'app_server_stopped');
  assert.match(failed.error.message,/exited with code 1/);
  const count=sent.length,refused=await bridge.request('thread/read',{},60000);
  assert.equal(refused.error.code,'app_server_down');
  assert.equal(sent.length,count);
  // A restarted app-server serves requests again.
  emit({type:'codex-app-server-connection-changed',hostId:'local',state:'connecting',transport:'stdio'});
  assert.equal(local().state,'connecting');
  emit({type:'codex-app-server-initialized',hostId:'local',transport:'stdio',appServerVersion:'0.171.0',installedCodexVersion:'0.171.0'});
  assert.deepEqual([local().state,local().appServerVersion,local().fatal],['connected','0.171.0',null]);
  bridge.request('thread/read',{},60000);
  await tick();
  assert.equal(sent.length,count+1);
  // A fatal app-server refuses new requests as unsent.
  emit({type:'codex-app-server-fatal-error',hostId:'local',errorMessage:'exited'});
  assert.equal((await bridge.request('thread/read',{},60000)).error.code,'app_server_down');
  emit({type:'codex-app-server-initialized',hostId:'local',transport:'stdio',appServerVersion:'0.171.0'});
  // A hook a newer Agenthail installs keeps what the earlier one heard.
  emit({type:'codex-app-server-fatal-error',hostId:'local',errorMessage:'gone'});
  vm.runInContext('globalThis.__agenthailCodexDesktopRendererV2.abi="old"',context);
  assert.equal(vm.runInContext(programs.hook,context),'hooked');
  assert.equal(local().fatal,'gone');
  process.exit(0);
})().catch(error=>{console.error(error);process.exit(1)});
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}

func TestCodexDesktopBridgeWaitsForDesktopsStateBeforeItsFirstRequest(t *testing.T) {
	node := testnode.Path(t)
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "hosts": codexHostsJS})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8'));
const hooked=()=>{const listeners=[],sent=[];const context=vm.createContext({window:{addEventListener:(name,f)=>listeners.push(f)},electronBridge:{sendMessageFromView:m=>{sent.push(m)}},setTimeout,clearTimeout});assert.equal(vm.runInContext(programs.hook,context),'hooked');return {context,sent,emit:data=>listeners[0]({data}),bridge:context.__agenthailCodexDesktopRendererV2}};
(async()=>{
  // Desktop's snapshot says its app-server is down: the first request is never sent.
  let {context,sent,emit,bridge}=hooked();
  const first=bridge.request('thread/loaded/list',{},60000),hosts=vm.runInContext(programs.hosts,context);
  setTimeout(()=>emit({type:'codex-app-server-fatal-error',hostId:'local',errorMessage:'exited'}),50);
  assert.equal((await first).error.code,'app_server_down');
  assert.equal(JSON.parse(await hosts)[0].fatal,'exited');
  assert.deepEqual(sent.map(m=>m.type),['request-app-server-state']);
  // A Desktop that never answers delays only the first second.
  ({sent,bridge}=hooked());
  bridge.request('thread/loaded/list',{},60000);
  await new Promise(resolve=>setTimeout(resolve,1200));
  assert.equal(sent.at(-1).type,'mcp-request');
  const later=Date.now();
  bridge.request('thread/read',{},60000);
  await new Promise(resolve=>setTimeout(resolve,0));
  assert.equal(sent.filter(m=>m.type==='mcp-request').length,2);
  assert.ok(Date.now()-later<100);
  process.exit(0);
})().catch(error=>{console.error(error);process.exit(1)});
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}

func desktopWithLocalHost(t *testing.T, host string) *codextest.Desktop {
	t.Helper()
	desktop := codextest.StartDesktop(t, func(method string) string {
		if method == "thread/loaded/list" {
			return `{"result":{"data":[]}}`
		}
		return ""
	})
	desktop.SetHosts(`[` + host + `]`)
	return desktop
}

func TestCodexRuntimeStatusNamesDesktopsAppServer(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	desktop := desktopWithLocalHost(t, `{"hostId":"local","state":"connected","transport":"stdio","appServerVersion":"0.171.0-alpha.13"}`)
	status := surfacetest.InDaemon(NewCodex(desktop.URL)).RuntimeStatus(context.Background())
	want := surface.AppServer{HostID: "local", Version: "0.171.0-alpha.13", Transport: "stdio"}
	if !status.Reachable || status.AppServer == nil || *status.AppServer != want {
		t.Fatalf("status=%+v appServer=%+v", status, status.AppServer)
	}
}

func TestCodexRuntimeStatusShowsAReconnectingAppServerWithoutWaiting(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	desktop := desktopWithLocalHost(t, `{"hostId":"local","state":"connecting","transport":"stdio"}`)
	started := time.Now()
	status := surfacetest.InDaemon(NewCodex(desktop.URL)).RuntimeStatus(context.Background())
	if status.Reachable || !strings.Contains(status.Detail, "app-server is connecting") || time.Since(started) > time.Second {
		t.Fatalf("status=%+v after %s", status, time.Since(started))
	}
	for _, method := range desktop.Methods() {
		if method == "thread/loaded/list" {
			t.Fatalf("a reconnecting app-server was sent %s", method)
		}
	}
}

func TestCodexSendRefusedByAStoppedDesktopAppServerDidNotStart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, test := range []struct {
		name        string
		turnStart   string
		unavailable bool
	}{
		{name: "refused before it was sent", turnStart: `{"error":{"code":"app_server_down","message":"Codex Desktop app-server stopped: exited"}}`, unavailable: true},
		{name: "lost after it was sent", turnStart: `{"error":{"code":"app_server_stopped","message":"Codex Desktop app-server stopped: exited"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := startDesktopBridge(t, func(method string) string {
				switch method {
				case "thread/read":
					return `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true}}}`
				case "thread/loaded/list":
					return `{"result":{"data":["thread"]}}`
				case "thread/turns/list":
					return `{"result":{"data":[]}}`
				case "turn/start":
					return test.turnStart
				}
				return ""
			})
			session := surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: codexTransportDesktop}
			_, err := surfacetest.InDaemon(NewCodex(bridge.URL)).Send(context.Background(), &session, "hello")
			if err == nil || surface.IsDeliveryUnavailable(err) != test.unavailable || surface.IsDeliveryOutcomeUnknown(err) == test.unavailable {
				t.Fatalf("err=%v unavailable=%t unknown=%t", err, surface.IsDeliveryUnavailable(err), surface.IsDeliveryOutcomeUnknown(err))
			}
		})
	}
}

func TestADesktopVoiceCallIsRejectedOnlyByTheAppServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, test := range []struct {
		name     string
		start    string
		rejected bool
	}{
		{name: "app-server refused", start: `{"error":{"code":-32000,"message":"401 Unauthorized"}}`, rejected: true},
		{name: "app-server down before sending", start: `{"error":{"code":"app_server_down","message":"Codex Desktop app-server stopped: exited"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			bridge := startDesktopBridge(t, func(method string) string {
				switch method {
				case "thread/read":
					return `{"result":{"thread":{"id":"operator","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true}}}`
				case "thread/loaded/list":
					return `{"result":{"data":["operator"]}}`
				case "thread/realtime/start":
					return test.start
				}
				return ""
			})
			v := &CodexVoice{Codex: surfacetest.InDaemon(NewCodex(bridge.URL))}
			session := &surface.Session{ID: "operator", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: codexTransportDesktop}
			err := v.Request(context.Background(), session, "thread/realtime/start", voice.StartParams(session.ID, "call-a", "v=0 offer", false, ""))
			var rejected *voice.RejectedError
			if errors.As(err, &rejected) != test.rejected || surface.IsDeliveryUnavailable(err) == test.rejected {
				t.Fatalf("err=%v rejected=%t unavailable=%t", err, errors.As(err, &rejected), surface.IsDeliveryUnavailable(err))
			}
		})
	}
}
