package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const fetchStopServer = `
export default function (ah: any) {
  ah.action('stopped', async () => {
    const started = Date.now();
    try {
      await ah.fetch('app', '/slow', { signal: AbortSignal.timeout(300) });
      return { code: 'answered', ms: Date.now() - started };
    } catch (error: any) {
      return { code: error.code, message: error.message, ms: Date.now() - started };
    }
  });
}
`

func TestAnExtensionFetchStopsWhenItsSignalDoes(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() { close(release) })

	manifest := fmt.Sprintf(`{"label": "Fixture", "mounts": [{"name": "app", "label": "App", "upstream": %q, "entry": "/"}]}`, upstream.URL)
	f := startExtensionFixtureWith(t, map[string]string{"extension.json": manifest, "server.ts": fetchStopServer})
	t.Cleanup(f.d.extensions.mounts.close)
	if err := f.d.approveExtension("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := f.d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}

	status, body := f.action(t, "stopped", nil)
	var outcome struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Ms      int64  `json:"ms"`
	}
	if status != http.StatusOK || json.Unmarshal(body["result"], &outcome) != nil {
		t.Fatalf("the action answered %d %s", status, body)
	}
	if outcome.Code != "aborted" || outcome.Ms > 3000 {
		t.Fatalf("a fetch whose signal fired after 300 ms ended as %+v, want aborted within 3 s", outcome)
	}
}
