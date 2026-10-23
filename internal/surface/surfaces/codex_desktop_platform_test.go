package surfaces

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
)

func TestCodexProbesTheDefaultDesktopPortOnlyWhereDesktopRuns(t *testing.T) {
	restore := codexDesktopRunsHere
	t.Cleanup(func() { codexDesktopRunsHere = restore })

	codexDesktopRunsHere = true
	if got := NewCodex("").desktopURL; got != "ws://127.0.0.1:9231" {
		t.Fatalf("where Desktop runs the default endpoint is %q", got)
	}

	codexDesktopRunsHere = false
	if err := NewCodex("").DesktopReady(context.Background()); err == nil || !strings.Contains(err.Error(), "does not run on") {
		t.Fatalf("without a configured port: %v", err)
	}
	desktop := codextest.StartDesktop(t, func(method string) string {
		if method == "thread/loaded/list" {
			return `{"result":{"data":[]}}`
		}
		return ""
	})
	endpoint, err := url.Parse(desktop.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewCodex(endpoint.Port()).DesktopReady(context.Background()); err != nil || desktop.Lookups() != 1 {
		t.Fatalf("a configured port: err=%v lookups=%d", err, desktop.Lookups())
	}
}
