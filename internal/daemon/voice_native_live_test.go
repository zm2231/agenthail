//go:build voice_live

package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/voice"
)

// TestNativeVoiceHost runs the production API and pairing against live Codex in
// an isolated registry. It never launches a daemon or creates an agent. The
// Simulator connects through a dedicated Tailscale HTTPS proxy, as in production.
func TestNativeVoiceHost(t *testing.T) {
	if os.Getenv("AGENTHAIL_NATIVE_VOICE") != "1" {
		t.Skip("requires explicit native voice evaluation")
	}
	directory := os.Getenv("AGENTHAIL_NATIVE_VOICE_DIRECTORY")
	if !filepath.IsAbs(directory) {
		t.Fatal("an absolute isolated evaluation directory is required")
	}
	origin, err := url.Parse(os.Getenv("AGENTHAIL_NATIVE_VOICE_HTTPS_ORIGIN"))
	if err != nil || origin.Scheme != "https" || !strings.HasSuffix(origin.Hostname(), ".ts.net") || origin.Port() == "" {
		t.Fatal("a dedicated Tailscale HTTPS origin with explicit test port is required")
	}
	data, err := os.ReadFile(os.Getenv("AGENTHAIL_VOICE_SMOKE_STATE"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		State voice.State `json:"state"`
	}
	if json.Unmarshal(data, &saved) != nil || saved.State.Session == nil || saved.State.Phase != "ended" {
		t.Fatal("an existing test-owned operator with confirmed ended audio is required")
	}
	voiceDirectory := filepath.Join(directory, "voice")
	if err := os.MkdirAll(voiceDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(voiceDirectory, "operator.json")
	if _, err := os.Stat(statePath); err == nil {
		t.Fatal("use a new isolated directory; do not overwrite operator state")
	}
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Open(filepath.Join(directory, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	codex := surfaces.NewCodex(os.Getenv("AGENTHAIL_CODEX_REMOTE"))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	sessions, err := codex.List(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if err := reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.RegisterSession(*saved.State.Session); err != nil {
		t.Fatal(err)
	}
	d := New(reg, []surface.Surface{codex})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: uuid.NewString()}))
	defer server.Close()
	defer server.CloseClientConnections()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		provider := &surfaces.CodexVoice{Codex: codex}
		if err := provider.Request(ctx, saved.State.Session, "thread/realtime/stop", map[string]any{"threadId": saved.State.Session.ID}); err != nil {
			t.Errorf("native test hangup: %v", err)
		}
	}()
	pairing, err := reg.CreateDevicePairing("Voice evaluation Simulator", []string{"read", "control"}, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"endpoint": {origin.String()}, "secret": {pairing.Secret}, "scope": {"read", "control"}}
	setup, err := json.Marshal(map[string]string{"endpoint": origin.String(), "upstream": server.URL, "pairingURL": "agenthail://pair?" + values.Encode()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "pairing.json"), setup, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Native API upstream ready at %s. Configure the dedicated HTTPS proxy to %s; pairing link: %s. No browser harness.", server.URL, origin.String(), directory)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	deadline := time.After(45 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatal("native evaluation window ended without an operator finish marker")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(directory, "finished")); err == nil {
				t.Log("Native host closed by evaluator; scenario acceptance requires actual UI, voice, and worker receipts.")
				return
			}
		}
	}
}
