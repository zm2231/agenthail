package surface

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestARequestsDetailSaysWhatItAsksToDo(t *testing.T) {
	input := func(value any) json.RawMessage {
		encoded, _ := json.Marshal(value)
		return encoded
	}
	long := strings.Repeat("line\n", 40)
	for _, c := range []struct {
		name    string
		request Request
		want    []string
		not     []string
	}{
		{"a shell command and where it runs", Request{Kind: WaitingApproval, Tool: "Bash", Input: input(map[string]any{"command": "rm -rf build", "description": "clean", "cwd": "/repo"})}, []string{"rm -rf build", "(in /repo)"}, []string{"clean"}},
		{"an edit's path, size and lines", Request{Kind: WaitingApproval, Tool: "Edit", Input: input(map[string]any{"file_path": "/repo/a.go", "old_string": "x := 1\n", "new_string": "x := 2\ny := 3\n"})}, []string{"/repo/a.go: +2 -1 lines", "- x := 1", "+ x := 2", "+ y := 3"}, nil},
		{"every edit of a multi-edit", Request{Kind: WaitingApproval, Tool: "MultiEdit", Input: input(map[string]any{"file_path": "/repo/a.go", "edits": []any{map[string]any{"old_string": "a", "new_string": "b"}, map[string]any{"old_string": "c", "new_string": "d"}}})}, []string{"/repo/a.go: +2 -2 lines in 2 edits", "- c", "+ d"}, nil},
		{"a long write leads with its size", Request{Kind: WaitingApproval, Tool: "Write", Input: input(map[string]any{"file_path": "/repo/b.txt", "content": long})}, []string{"/repo/b.txt: +40 -0 lines", "… 28 more lines"}, nil},
		{"every field of a fetch", Request{Kind: WaitingApproval, Tool: "WebFetch", Input: input(map[string]any{"url": "https://example.com", "prompt": "summarize the pricing"})}, []string{`"url":"https://example.com"`, `"prompt":"summarize the pricing"`}, nil},
		{"any other tool's input", Request{Kind: WaitingApproval, Tool: "mcp__x__deploy", Input: input(map[string]any{"env": "prod", "force": true})}, []string{`{"env":"prod","force":true}`}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			detail := c.request.Detail()
			for _, want := range c.want {
				if !strings.Contains(detail, want) {
					t.Fatalf("detail %q does not say %q", detail, want)
				}
			}
			for _, not := range c.not {
				if strings.Contains(detail, not) {
					t.Fatalf("detail %q says %q", detail, not)
				}
			}
		})
	}
	question := Request{Kind: WaitingQuestion, Tool: "AskUserQuestion", Input: input(map[string]any{"questions": []any{}})}
	if detail := question.Detail(); detail != "" {
		t.Fatalf("a question has its own text, not a detail: %q", detail)
	}
}
