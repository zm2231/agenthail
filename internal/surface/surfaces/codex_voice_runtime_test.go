package surfaces

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/zm2231/agenthail/internal/testnode"
)

func TestDesktopVoiceCursorRuntime(t *testing.T) {
	node := testnode.Path(t)
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "voice": codexVoiceEventsJS(0, "operator", 0), "all": codexEventsJS(0), "response": codexDynamicToolResponseJS("request-1", "operator", true, "Transferred")})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8')),listeners=[];
const sent=[];
// Desktop's main process reads message.response.id for a view's mcp-response.
const host={sendMessageFromView:message=>{if(message.type==='mcp-response')void message.response.id;sent.push(message)}};
const context=vm.createContext({window:{addEventListener:(name,f)=>listeners.push(f)},electronBridge:host,setTimeout,clearTimeout});
assert.equal(vm.runInContext(programs.hook,context),'hooked');
assert.equal(vm.runInContext(programs.hook,context),'already');
assert.equal(listeners.length,1);
(async()=>{
assert.deepEqual(JSON.parse(await vm.runInContext(programs.voice,context)),{cursor:0,lost:false,events:[]});
const emit=data=>listeners[0]({data});
emit({type:'mcp-notification',hostId:'remote',method:'thread/realtime/sdp',params:{threadId:'operator',sdp:'private'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/started',params:{threadId:'operator',realtimeSessionId:'call'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/sdp',params:{threadId:'other',sdp:'other'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/sdp',params:{threadId:'operator',sdp:'v=0 answer'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/outputAudio/delta',params:{threadId:'operator',audio:'excluded'}});
emit({type:'mcp-notification',hostId:'local',method:'turn/started',params:{threadId:'operator'}});
emit({type:'mcp-request',hostId:'local',request:{id:'request-1',method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_transfer',arguments:{targetId:'target'}}}});
const result=JSON.parse(await vm.runInContext(programs.voice,context));
assert.equal(result.cursor,6);assert.equal(result.lost,false);
assert.deepEqual(result.events.map(e=>e.method),['thread/realtime/started','thread/realtime/sdp','turn/started','item/tool/call']);
assert.equal(result.events[1].params.sdp,'v=0 answer');
assert.equal(result.events[3].params.requestId,'request-1');
assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,6);
  assert.equal(await vm.runInContext(programs.response,context),'ok');
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])),{type:'mcp-response',hostId:'local',threadId:'operator',response:{id:'request-1',result:{success:true,contentItems:[{type:'inputText',text:'Transferred'}]}}});
  assert.match(await vm.runInContext(programs.response,context),/no longer available/);
  const bridge=context.__agenthailCodexDesktopRendererV2;
  // A waiting poll returns on the first event for its thread, not at its bound.
  const started=Date.now(),waiting=bridge.voiceEvents(bridge.sequence,'operator',5000);
  let settled=false;waiting.then(()=>{settled=true});
  emit({type:'mcp-notification',hostId:'local',method:'turn/started',params:{threadId:'other'}});
  emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/outputAudio/delta',params:{threadId:'operator'}});
  await new Promise(resolve=>setTimeout(resolve,20));
  assert.equal(settled,false);
  emit({type:'mcp-notification',hostId:'local',method:'turn/started',params:{threadId:'operator'}});
  assert.equal(JSON.stringify((await waiting).events.map(e=>e.method)),'["turn/started"]');
  assert.ok(Date.now()-started<1000);
  assert.equal(bridge.waiters.size,0);
  const quiet=await bridge.voiceEvents(bridge.sequence,'operator',10);
  assert.deepEqual([quiet.events.length,quiet.lost],[0,false]);
  assert.equal(bridge.waiters.size,0);
  emit({type:'mcp-request',hostId:'local',request:{id:42,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_projects',arguments:{}}}});
  await assert.rejects(bridge.respondDynamicToolCall('42','other',true,'[]'),/thread does not match/);
  await bridge.respondDynamicToolCall('42','operator',true,'[]');
  assert.equal(sent[1].response.id,42);
  for(let i=0;i<1001;i++)emit({type:'mcp-notification',hostId:'local',method:'turn/progress',params:{threadId:'operator'}});
  assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,1000);
  assert.equal(JSON.parse(await vm.runInContext(programs.voice,context)).lost,true);

  // A request being answered stays answerable however many follow it.
  emit({type:'mcp-request',hostId:'local',request:{id:900,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_send_to_lead'}}});
  for(let i=0;i<300;i++)emit({type:'mcp-request',hostId:'local',request:{id:1000+i,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_projects'}}});
  await bridge.respondDynamicToolCall('900','operator',true,'sent');
  assert.equal(sent[sent.length-1].response.id,900);
  // An expired request is neither offered to the service nor answerable.
  bridge.toolRequests.get('1000').at=0;
  emit({type:'mcp-request',hostId:'local',request:{id:2000,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_projects'}}});
  const offered=JSON.parse(await vm.runInContext(programs.voice,context)).events.filter(e=>e.method==='item/tool/call').map(e=>e.params.requestId);
  assert.equal(offered.includes('1000'),false);
  assert.equal(offered.includes('2000'),true);
  await assert.rejects(bridge.respondDynamicToolCall('1000','operator',true,'[]'),/no longer available/);
  await bridge.respondDynamicToolCall('1299','operator',true,'[]');
  assert.equal(sent[sent.length-1].response.id,1299);

  // A hook an earlier daemon installed is replaced, keeping its event cursor.
  const oldListeners=[],oldSent=[];
  const upgraded=vm.createContext({window:{addEventListener:(name,f)=>oldListeners.push(f)},electronBridge:{sendMessageFromView:message=>{if(message.type==='mcp-response')void message.response.id;oldSent.push(message)}},setTimeout,clearTimeout});
  vm.runInContext("globalThis.__agenthailCodexDesktopRendererV2={abi:2,toolRequests:new Map([['77',{id:77,threadId:'operator'}]]),events:[{sequence:5,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_send_to_lead',requestId:'77'}},{sequence:6,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_send_to_lead',requestId:'5'}},{sequence:7,method:'turn/started',params:{threadId:'operator'}}],sequence:7,next:3,pending:new Map(),request(){}}",upgraded);
  let woken=0;
  upgraded.__agenthailCodexDesktopRendererV2.waiters=vm.runInContext('new Set()',upgraded).add(()=>{woken++});
  assert.equal(vm.runInContext(programs.hook,upgraded),'hooked');
  // A poll the replaced bridge was holding is released instead of waiting out its bound.
  assert.equal(woken,1);
  assert.equal(vm.runInContext(programs.hook,upgraded),'already');
  const fresh=upgraded.__agenthailCodexDesktopRendererV2;
  assert.equal(fresh.sequence,7);
  assert.notEqual(fresh.abi,2);
  // A request the earlier hook recorded stays answerable; one it kept no way to answer is not offered.
  assert.deepEqual(JSON.parse(await vm.runInContext(programs.voice,upgraded)).events.map(e=>[e.method,e.params.requestId]),[['item/tool/call','77'],['turn/started',undefined]]);
  await fresh.respondDynamicToolCall('77','operator',true,'sent');
  assert.equal(oldSent[0].response.id,77);
  oldSent.length=0;
  oldListeners[oldListeners.length-1]({data:{type:'mcp-request',hostId:'local',request:{id:9,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_projects'}}}});
  assert.equal(fresh.sequence,8);
  await fresh.respondDynamicToolCall('9','operator',true,'[]');
  assert.equal(oldSent[0].response.id,9);
})().catch(error=>{console.error(error);process.exit(1)});
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}
