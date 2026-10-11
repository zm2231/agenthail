package surfaces

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

func TestDesktopVoiceCursorRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to execute the renderer bridge regression")
	}
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "voice": codexVoiceEventsJS(0, "operator"), "all": codexEventsJS(0), "response": codexDynamicToolResponseJS("request-1", "operator", true, "Transferred")})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8')),listeners=[];
const sent=[];let rejectResponse=false;
const context=vm.createContext({window:{addEventListener:(name,f)=>listeners.push(f)},electronBridge:{sendMessageFromView:message=>{
  if(message.type==='mcp-response') {
    assert.notEqual(message.response,undefined);
    assert.equal(message.message,undefined);
    assert.equal(message.threadId,'operator');
    assert.notEqual(message.response.id,undefined);
    if(rejectResponse)throw Error('fixture host rejected response');
  }
  sent.push(message);
}},setTimeout,clearTimeout});
assert.equal(vm.runInContext(programs.hook,context),'hooked');
assert.equal(vm.runInContext(programs.hook,context),'already');
assert.equal(listeners.length,1);
assert.deepEqual(JSON.parse(vm.runInContext(programs.voice,context)),{cursor:0,lost:false,events:[]});
const emit=data=>listeners[0]({data});
emit({type:'mcp-notification',hostId:'remote',method:'thread/realtime/sdp',params:{threadId:'operator',sdp:'private'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/started',params:{threadId:'operator',realtimeSessionId:'call'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/sdp',params:{threadId:'other',sdp:'other'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/sdp',params:{threadId:'operator',sdp:'v=0 answer'}});
emit({type:'mcp-notification',hostId:'local',method:'thread/realtime/outputAudio/delta',params:{threadId:'operator',audio:'excluded'}});
emit({type:'mcp-notification',hostId:'local',method:'turn/started',params:{threadId:'operator'}});
emit({type:'mcp-request',hostId:'local',request:{id:'request-1',method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_transfer',arguments:{targetId:'target'}}}});
const result=JSON.parse(vm.runInContext(programs.voice,context));
assert.equal(result.cursor,6);assert.equal(result.lost,false);
assert.deepEqual(result.events.map(e=>e.method),['turn/started','item/tool/call']);
assert.equal(result.events[1].params.requestId,'request-1');
assert.equal(result.events.some(e=>e.method.startsWith('thread/realtime/')),false);
vm.runInContext(programs.response,context).then(value=>{
  assert.equal(value,'ok');
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])),{type:'mcp-response',hostId:'local',threadId:'operator',response:{id:'request-1',result:{success:true,contentItems:[{type:'inputText',text:'Transferred'}]}}});
  emit({type:'mcp-request',hostId:'local',request:{id:42,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_transfer',arguments:{targetId:'target'}}}});
  return awaitResponse();
}).catch(error=>{throw error});
async function awaitResponse(){
  await context.__agenthailCodexDesktopRendererV2.respondDynamicToolCall('42','operator',true,'Transferred');
  assert.equal(sent[1].response.id,42);
  const bridge=context.__agenthailCodexDesktopRendererV2;
  const sequence=bridge.sequence;
  emit({type:'mcp-request',hostId:'local',request:{id:42,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'voice_transfer'}}});
  assert.equal(bridge.sequence,sequence);
  await bridge.respondDynamicToolCall('42','operator',true,'Transferred');
  assert.equal(sent.length,2);
  emit({type:'mcp-request',hostId:'local',request:{id:43,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'session_list'}}});
  const pendingSequence=bridge.sequence;
  emit({type:'mcp-request',hostId:'local',request:{id:43,method:'item/tool/call',params:{threadId:'operator',namespace:'agenthail',tool:'session_list'}}});
  assert.equal(bridge.sequence,pendingSequence);
  rejectResponse=true;
  await assert.rejects(bridge.respondDynamicToolCall('43','operator',true,'[]'),/fixture host rejected/);
  assert.equal(bridge.toolRequests.has('43'),true);
  rejectResponse=false;
  await assert.rejects(bridge.respondDynamicToolCall('43','wrong-thread',true,'[]'),/thread does not match/);
  await bridge.respondDynamicToolCall('43','operator',true,'[]');
  assert.equal(sent.length,3);
}
assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,6);
for(let i=0;i<1001;i++)emit({type:'mcp-notification',hostId:'local',method:'turn/progress',params:{threadId:'operator'}});
assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,1000);
assert.equal(JSON.parse(vm.runInContext(programs.voice,context)).lost,true);
const upgraded=vm.createContext({window:{addEventListener:()=>{}},electronBridge:{sendMessageFromView:message=>{
  assert.equal(message.response.id,99);
  assert.equal(message.threadId,'operator');
  assert.equal(message.response.result.success,true);
}},setTimeout,clearTimeout});
vm.runInContext("globalThis.__agenthailCodexDesktopRendererV2={abi:3,next:8,sequence:1042,pending:new Map(),toolRequests:new Map([['99',99]]),events:Array.from({length:1000},(_,i)=>({sequence:43+i,method:'turn/progress',params:{threadId:'operator'}}))}",upgraded);
assert.equal(vm.runInContext(programs.hook,upgraded),'hooked');
assert.equal(upgraded.__agenthailCodexDesktopRendererV2.sequence,1042);
upgraded.__agenthailCodexDesktopRendererV2.respondDynamicToolCall('99','operator',true,'Transferred').catch(error=>{throw error});
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}
