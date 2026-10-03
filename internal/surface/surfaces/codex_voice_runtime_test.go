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
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "voice": codexVoiceEventsJS(0, "operator"), "all": codexEventsJS(0), "response": codexDynamicToolResponseJS("request-1", true, "Transferred")})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8')),listeners=[];
const sent=[];const context=vm.createContext({window:{addEventListener:(name,f)=>listeners.push(f)},electronBridge:{sendMessageFromView:message=>{sent.push(message)}},setTimeout,clearTimeout});
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
assert.deepEqual(result.events.map(e=>e.method),['thread/realtime/started','thread/realtime/sdp','turn/started','item/tool/call']);
assert.equal(result.events[1].params.sdp,'v=0 answer');
assert.equal(result.events[3].params.requestId,'request-1');
vm.runInContext(programs.response,context).then(value=>{
  assert.equal(value,'ok');
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])),{type:'mcp-response',hostId:'local',message:{id:'request-1',result:{success:true,contentItems:[{type:'inputText',text:'Transferred'}]}}});
}).catch(error=>{throw error});
assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,6);
for(let i=0;i<1001;i++)emit({type:'mcp-notification',hostId:'local',method:'turn/progress',params:{threadId:'operator'}});
assert.equal(JSON.parse(vm.runInContext(programs.all,context)).events.length,1000);
assert.equal(JSON.parse(vm.runInContext(programs.voice,context)).lost,true);
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}
