package surfaces

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"
)

func TestDesktopNotificationBridgeRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to execute the renderer bridge regression")
	}
	programs, err := json.Marshal(map[string]string{"hook": codexHookJS, "events": codexEventsJS(0)})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "-e", `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const programs=JSON.parse(fs.readFileSync(0,'utf8')),listeners=[];
const context=vm.createContext({window:{addEventListener:(_,f)=>listeners.push(f)},electronBridge:{sendMessageFromView:()=>{}},setTimeout,clearTimeout});
assert.equal(vm.runInContext(programs.hook,context),'hooked');
assert.equal(vm.runInContext(programs.hook,context),'already');
assert.equal(listeners.length,1);
const emit=data=>listeners[0]({data});
emit({type:'mcp-notification',hostId:'remote',method:'turn/started',params:{threadId:'remote'}});
emit({type:'mcp-notification',hostId:'local',method:'turn/started',params:{threadId:'local-task',turn:{id:'turn-a'}}});
let result=JSON.parse(vm.runInContext(programs.events,context));
assert.deepEqual(result,{cursor:1,events:[{sequence:1,method:'turn/started',params:{threadId:'local-task',turn:{id:'turn-a'}}}]});
for(let i=0;i<1001;i++)emit({type:'mcp-notification',hostId:'local',method:'turn/progress',params:{threadId:'local-task'}});
result=JSON.parse(vm.runInContext(programs.events,context));
assert.equal(result.cursor,1002);assert.equal(result.events.length,1000);assert.equal(result.events[0].sequence,3);
`)
	cmd.Stdin = bytes.NewReader(programs)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("renderer execution: %v\n%s", err, output)
	}
}
