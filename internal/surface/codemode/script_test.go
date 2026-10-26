package codemode

import (
	"reflect"
	"testing"
)

func TestAScriptReadsAsTheToolItCalls(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
		row    Row
		calls  int
	}{
		{"quoted keys", `const r = await tools.exec_command({"cmd":"go test ./...","workdir":"/work/demo","yield_time_ms":30000});
text(r.output);`, Row{Tool: "exec_command", Input: map[string]string{"cmd": "go test ./...", "workdir": "/work/demo"}}, 1},
		{"bare keys", "text(await tools.exec_command({cmd:\"ls -la\", workdir: `/work/demo`}))", Row{Tool: "exec_command", Input: map[string]string{"cmd": "ls -la", "workdir": "/work/demo"}}, 1},
		{"namespaced tool", `text(await tools.multi_agent_v1__spawn_agent({message:"Review the parser"}))`, Row{Tool: "spawn_agent", Input: map[string]string{"message": "Review the parser"}}, 1},
		{"several calls", `const a = await tools.exec_command({cmd:"make"});
const b = await tools.write_stdin({session_id: a.session_id, chars: ""});
text(b.output);`, Row{Tool: "exec_command", Input: map[string]string{"cmd": "make"}}, 2},
		{"no readable fields", `const x = await tools.mcp__demo__lookup(args); text(x)`, Row{Tool: "mcp__demo__lookup", Input: map[string]string{"description": "Calls mcp · demo · lookup"}}, 1},
		{"unparsable script keeps names", `const r = await tools.exec_command({cmd:"ls" ; tools.wait(`, Row{Tool: "exec_command", Input: map[string]string{"description": "Calls exec_command, wait"}}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls, ok := Read(test.script)
			if !ok || len(calls) != test.calls {
				t.Fatalf("Read = %+v, %v", calls, ok)
			}
			if row := Present(calls); !reflect.DeepEqual(row, test.row) {
				t.Fatalf("row = %+v, want %+v", row, test.row)
			}
		})
	}
}

func TestPlainInputIsNotCodeMode(t *testing.T) {
	for _, input := range []string{`{"cmd":"ls"}`, "*** Begin Patch\n*** End Patch", `console.log("tools")`, ""} {
		if calls, ok := Read(input); ok {
			t.Fatalf("Read(%q) = %+v", input, calls)
		}
	}
}
