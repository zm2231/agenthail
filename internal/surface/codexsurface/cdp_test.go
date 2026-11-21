package codexsurface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
