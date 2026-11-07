package surfaces

import (
	"os"
	"path/filepath"
	"testing"
)

func TestACompactIsNotCreditedWithABoundaryFromAnotherPrompt(t *testing.T) {
	const request = "req"
	ownBoundary := `{"type":"system","subtype":"compact_boundary","uuid":"b2","parentUuid":null}
{"type":"user","uuid":"s2","parentUuid":"b2","promptId":"mine","isCompactSummary":true,"message":{"content":"summary"}}
`
	cases := []struct {
		name      string
		records   string
		completed bool
		rejected  string
	}{
		{
			name: "Claude compacted on its own just before and then refused the request",
			records: `{"type":"system","subtype":"compact_boundary","uuid":"b1","parentUuid":null}
{"type":"user","uuid":"s1","parentUuid":"b1","promptId":"other","isCompactSummary":true,"message":{"content":"summary"}}
{"type":"user","uuid":"req","parentUuid":"s1","promptId":"mine","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
{"type":"system","subtype":"local_command","uuid":"e","parentUuid":"req","content":"<local-command-stderr>Error: No messages to compact</local-command-stderr>"}
`,
			rejected: "Error: No messages to compact",
		},
		{
			name: "a request without a promptId after a boundary",
			records: ownBoundary + `{"type":"user","uuid":"req","parentUuid":"s2","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
`,
		},
		{
			name: "the request's own boundary",
			records: ownBoundary + `{"type":"user","uuid":"req","parentUuid":"s2","promptId":"mine","message":{"content":"<command-name>/compact</command-name>\n<command-args></command-args>"}}
`,
			completed: true,
		},
		{
			name: "a boundary after the request, over Remote Control",
			records: `{"type":"user","uuid":"req","message":{"content":"/compact"}}
{"type":"system","subtype":"compact_boundary","uuid":"b3","parentUuid":null}
`,
			completed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			if err := os.WriteFile(path, []byte(tc.records), 0o600); err != nil {
				t.Fatal(err)
			}
			completed, rejected, err := readClaudeCompactCompletion(path, 0, request)
			if err != nil || completed != tc.completed || rejected != tc.rejected {
				t.Fatalf("completed=%v rejected=%q err=%v, want completed=%v rejected=%q", completed, rejected, err, tc.completed, tc.rejected)
			}
		})
	}
}
