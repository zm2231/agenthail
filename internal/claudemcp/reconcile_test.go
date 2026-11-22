package claudemcp

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestReconcileNeverRewritesAnEntryThePersonEdited(t *testing.T) {
	const now, old = "/opt/agenthail/bin/agenthail", "/usr/local/bin/agenthail"
	written := `{"mcpServers":{"agenthail":` + string(entry(old)) + `}}`
	edited := `{"mcpServers":{"agenthail":{"type":"stdio","command":"` + old + `","args":["mcp","stdio","--verbose"],"env":{"AGENTHAIL_MCP_INSTALLED_BY":"agenthail mcp install"}}}}`
	taken := `{"mcpServers":{"agenthail":{"type":"http","url":"http://127.0.0.1:9/mcp"}}}`
	for _, test := range []struct {
		name, config string
		off          bool
		want         string
	}{
		{"missing is added", othersConfig, false, string(entry(now))},
		{"install's own form follows the binary", written, false, string(entry(now))},
		{"a hand-edited entry stays", edited, false, gjson.Get(edited, key).Raw},
		{"a server the person added stays", taken, false, gjson.Get(taken, key).Raw},
		{"off removes install's own form", written, true, ""},
		{"off keeps a hand-edited entry", edited, true, gjson.Get(edited, key).Raw},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfig(t, test.config)
			if _, err := ReconcileAt(path, now, test.off); err != nil {
				t.Fatal(err)
			}
			got := gjson.GetBytes(read(t, path), key)
			if test.want == "" && got.Exists() || test.want != "" && !sameJSON(got.Raw, []byte(test.want)) {
				t.Fatalf("entry = %s, want %s", got.Raw, test.want)
			}
			if changed, err := ReconcileAt(path, now, test.off); err != nil || changed {
				t.Fatalf("a second reconcile = %v, %v", changed, err)
			}
		})
	}
}
