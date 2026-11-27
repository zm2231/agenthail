package codexsurface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

func rendererTargets(t *testing.T, pages ...map[string]string) ([]codexCDPTarget, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(pages)
	}))
	t.Cleanup(server.Close)
	return resolveCodexRendererEndpoint(context.Background(), "ws"+server.URL[len("http"):])
}

func TestCodexDesktopBridgeNeverHooksTheAvatarOverlay(t *testing.T) {
	overlay := map[string]string{"type": "page", "url": "app://-/index.html?view=avatar-overlay", "webSocketDebuggerUrl": "ws://overlay"}
	main := map[string]string{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws://main"}
	targets, err := rendererTargets(t, overlay, main)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].wsURL != "ws://main" {
		t.Fatalf("targets=%+v", targets)
	}
	if targets, err := rendererTargets(t, overlay); err == nil {
		t.Fatalf("an overlay alone was offered as the bridge: %+v", targets)
	}
}

func TestADesktopThatNeverAnswersReleasesTheCallerWhenItsContextEnds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stalled := make(chan struct{})
	bridge := startDesktopBridge(t, func(method string) string {
		if method == "thread/resume" {
			<-stalled
		}
		return `{"result":{}}`
	})
	t.Cleanup(func() { close(stalled) })
	client, err := surfacetest.InDaemon(NewCodex(bridge.URL)).openDesktop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err = client.Request(ctx, codexThreadResume, map[string]any{"threadId": "t"}, codexThreadLoadWait)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(began) > 5*time.Second {
		t.Fatalf("a resume Desktop never answered returned %v after %v", err, time.Since(began))
	}
}
