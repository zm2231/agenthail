package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Each request re-authenticates the minting credential, so a revoked device's web sessions end at once.
const (
	webSessionCookieName = "agenthail_web"
	webSessionLifetime   = 12 * time.Hour
	webSessionLimit      = 512
	webSessionPrefix     = "ahw_"
)

var webSessionScopes = []string{"read", "control"}

type webSession struct {
	minter    string
	scopes    []string
	expiresAt time.Time
}

type webSessions struct {
	mu   sync.Mutex
	byID map[string]webSession
	now  func() time.Time
}

type mountPortContextKey struct{}

func (s *webSessions) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *webSessions) mint(minter string, scopes []string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	value := webSessionPrefix + base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.byID == nil {
		s.byID = map[string]webSession{}
	}
	for id, session := range s.byID {
		if !now.Before(session.expiresAt) {
			delete(s.byID, id)
		}
	}
	for len(s.byID) >= webSessionLimit {
		oldest := ""
		for id, session := range s.byID {
			if oldest == "" || session.expiresAt.Before(s.byID[oldest].expiresAt) {
				oldest = id
			}
		}
		delete(s.byID, oldest)
	}
	expiresAt := now.Add(webSessionLifetime).UTC()
	s.byID[webSessionKey(value)] = webSession{minter: minter, scopes: scopes, expiresAt: expiresAt}
	return value, expiresAt, nil
}

func (s *webSessions) find(value string) (webSession, bool) {
	if !strings.HasPrefix(value, webSessionPrefix) {
		return webSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := webSessionKey(value)
	session, ok := s.byID[key]
	if ok && !s.clock().Before(session.expiresAt) {
		delete(s.byID, key)
		return webSession{}, false
	}
	return session, ok
}

func webSessionKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func webSessionPath(r *http.Request) bool {
	if onMountPort(r) {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/x/") {
		return true
	}
	return r.URL.Path == "/api/v1/events" && (r.Method == http.MethodGet || r.Method == http.MethodHead)
}

func onMountPort(r *http.Request) bool {
	on, _ := r.Context().Value(mountPortContextKey{}).(bool)
	return on
}

type webSessionResult int

const (
	webSessionAbsent webSessionResult = iota
	webSessionDenied
	webSessionForbidden
	webSessionAllowed
)

func (d *Daemon) authorizeWebSession(dashboard *dashboardServer, r *http.Request, scope string) (*http.Request, webSessionResult) {
	cookie, err := r.Cookie(webSessionCookieName)
	if err != nil || cookie.Value == "" {
		return r, webSessionAbsent
	}
	session, ok := d.webSessions.find(cookie.Value)
	if !ok || !webSessionPath(r) {
		return r, webSessionDenied
	}
	if !slices.Contains(session.scopes, scope) {
		return r, webSessionForbidden
	}
	if constantTokenEqual(session.minter, dashboard.token) {
		return r, webSessionAllowed
	}
	device, err := d.Registry.AuthenticateDevice(session.minter, scope)
	if err != nil {
		return r, webSessionDenied
	}
	ctx := context.WithValue(r.Context(), actionPrincipalContextKey{}, device.ID)
	ctx = context.WithValue(ctx, apiHostPrincipalContextKey{}, apiHostPrincipal{host: device.IsHost(), hostID: device.HostID, hostName: device.Name})
	return r.WithContext(ctx), webSessionAllowed
}

type webSessionView struct {
	Name      string    `json:"name"`
	Value     string    `json:"value"`
	Path      string    `json:"path"`
	ExpiresAt time.Time `json:"expiresAt"`
	Scopes    []string  `json:"scopes"`
}

func (d *Daemon) apiWebSessionHandler(dashboard *dashboardServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeAPIError(w, http.StatusForbidden, "bearer_required", "Only a device or dashboard token can start a web session.")
			return
		}
		scopes := slices.Clone(webSessionScopes)
		if !constantTokenEqual(token, dashboard.token) {
			scopes = slices.DeleteFunc(scopes, func(scope string) bool {
				_, err := d.Registry.AuthenticateDevice(token, scope)
				return err != nil
			})
			if len(scopes) == 0 {
				writeAPIError(w, http.StatusUnauthorized, "unauthorized", "A valid Agenthail device token is required.")
				return
			}
		}
		value, expiresAt, err := d.webSessions.mint(token, scopes)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "The web session could not be created.")
			return
		}
		writeDashboardJSON(w, http.StatusOK, webSessionView{Name: webSessionCookieName, Value: value, Path: "/", ExpiresAt: expiresAt, Scopes: scopes})
	}
}

