package daemon

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
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
	s := voice.NewWithTargetsAndOperatorSourceAndStream(filepath.Join(filepath.Dir(d.Registry.Path()), "voice", "operator.json"), provider, d.Registry.RegisterSession, commandPath, d.resolveVoiceTarget, d.dispatcher(), func(session *surface.Session, active bool) {
		d.setSessionSourceHold(session, active, "voice")
	}, daemonVoiceStreamProvider{daemon: d})
	dashboard.voice = s
	mux.HandleFunc("/api/v1/voice", d.voiceBearerGuard(dashboard, voiceHandler(s)))
	mux.HandleFunc("/api/v1/voice/peer", d.voiceBearerGuard(dashboard, voicePeerHandler))
	mux.HandleFunc("/api/voice", dashboard.guard(dashboardVoiceHandler(s, dashboard.token)))
	mux.HandleFunc("/voice-peer.js", dashboard.guard(voicePeerScriptHandler))
}

type daemonVoiceStreamProvider struct {
	daemon *Daemon
}

func (p daemonVoiceStreamProvider) PrepareSessionStream(ctx context.Context, session *surface.Session) (sessionstream.Subscription, error) {
	if p.daemon == nil || session == nil {
		return sessionstream.Subscription{}, fmt.Errorf("voice session source is unavailable")
	}
	adapter := p.daemon.surfaceForKind(session.Surface)
	if adapter == nil {
		return sessionstream.Subscription{}, fmt.Errorf("voice session source has no adapter")
	}
	return p.daemon.sources.prepareStream(ctx, session, adapter)
}

func (d *Daemon) resolveVoiceTarget(ctx context.Context, requested string) (*voice.Target, error) {
	if d.Registry == nil {
		return nil, fmt.Errorf("session-bound voice requires the Agenthail registry")
	}
	id, err := d.Registry.ResolveTarget(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve voice target: %w", err)
	}
	session, err := d.Registry.Session(id)
	if err != nil {
		return nil, fmt.Errorf("load voice target: %w", err)
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		return nil, fmt.Errorf("%s target has no configured adapter", session.Surface)
	}
	resolved, err := adapter.Resolve(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("verify voice target: %w", err)
	}
	if resolved == nil || resolved.ID != session.ID {
		return nil, fmt.Errorf("verified voice target did not retain its exact identity")
	}
	return &voice.Target{Session: resolved, Adapter: adapter}, nil
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
		voiceActionHandler(s, fmt.Sprintf("%x", sha256.Sum256([]byte(token))), w, r, true)
	}
}

func dashboardVoiceHandler(s *voice.Service, token string) http.HandlerFunc {
	owner := fmt.Sprintf("dashboard:%x", sha256.Sum256([]byte(token)))
	return func(w http.ResponseWriter, r *http.Request) {
		voiceActionHandler(s, owner, w, r, false)
	}
}

func voiceActionHandler(s *voice.Service, owner string, w http.ResponseWriter, r *http.Request, apiV1 bool) {
	switch r.Method {
	case http.MethodGet:
		writeDashboardJSON(w, http.StatusOK, s.View(owner))
	case http.MethodPost:
		var a voice.Action
		if apiV1 {
			if err := decodeAPIV1JSON(w, r, &a, true); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "Invalid voice request.")
				return
			}
		} else if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&a); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "Invalid voice request.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		state, err := s.Apply(ctx, owner, a)
		if err != nil {
			writeDashboardJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "voice_blocked", "message": err.Error()}, "state": state})
			return
		}
		writeDashboardJSON(w, http.StatusOK, state)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST.")
	}
}

func voicePeerScriptHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Permissions-Policy", "microphone=(self), camera=()")
	_, _ = fmt.Fprint(w, voicePeerScript)
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
