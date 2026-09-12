package daemon

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/voice"
)

//go:embed voice_peer.js
var voicePeerScript string

func (d *Daemon) registerVoiceAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	var provider voice.Provider
	if codex, ok := d.surfaceForKind(surface.KindCodex).(*surfaces.Codex); ok {
		provider = &surfaces.CodexVoice{Codex: codex}
	}
	commandPath, err := os.Executable()
	if err != nil {
		provider = nil
	}
	s := voice.New(filepath.Join(filepath.Dir(d.Registry.Path()), "voice", "operator.json"), provider, d.Registry.RegisterSession, commandPath)
	mux.HandleFunc("/api/v1/voice", d.voiceBearerGuard(dashboard, voiceHandler(s)))
	mux.HandleFunc("/api/v1/voice/peer", d.voiceBearerGuard(dashboard, voicePeerHandler))
}

func (d *Daemon) voiceBearerGuard(dashboard *dashboardServer, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeAPIError(w, 401, "device_required", "Voice requires a paired device token with control permission.")
			return
		}
		if !constantTokenEqual(token, dashboard.token) {
			if _, err := d.Registry.AuthenticateDevice(token, "control"); err != nil {
				writeAPIError(w, 401, "device_required", "Voice requires a paired device token with control permission.")
				return
			}
		}
		next(w, r)
	}
}

func voiceHandler(s *voice.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The native client authenticates each request. No browser cookie or Codex credential enters the media page.
		token := bearerToken(r)
		if token == "" {
			writeAPIError(w, 401, "device_required", "Voice requires the paired native client's Bearer token.")
			return
		}
		owner := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		switch r.Method {
		case http.MethodGet:
			writeDashboardJSON(w, 200, s.View(owner))
		case http.MethodPost:
			var a voice.Action
			if err := decodeAPIV1JSON(w, r, &a, true); err != nil {
				writeAPIError(w, 400, "invalid_request", "Invalid voice request.")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			state, err := s.Apply(ctx, owner, a)
			if err != nil {
				writeDashboardJSON(w, 409, map[string]any{"error": map[string]string{"code": "voice_blocked", "message": err.Error()}, "state": state})
				return
			}
			writeDashboardJSON(w, 200, state)
		default:
			writeAPIError(w, 405, "method_not_allowed", "Use GET or POST.")
		}
	}
}

func voicePeerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, 405, "method_not_allowed", "Use GET.")
		return
	}
	if bearerToken(r) == "" {
		writeAPIError(w, 401, "device_required", "The native voice client must authenticate this page.")
		return
	}
	hash := sha256.Sum256([]byte(voicePeerScript))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(hash[:])+"'; media-src blob:; connect-src https: wss:; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Permissions-Policy", "microphone=(self), camera=()")
	_, _ = fmt.Fprint(w, "<!doctype html><html><head><meta name=viewport content='width=device-width,initial-scale=1'></head><body><audio autoplay playsinline></audio><script>"+strings.ReplaceAll(voicePeerScript, "</script", "<\\/script")+"</script></body></html>")
}
