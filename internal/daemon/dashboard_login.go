package daemon

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// A login link signs one browser in once. Redeeming it creates a paired device
// for that browser, so its cookie can be revoked alone and no printed URL ever
// carries a token that outlives the link.
const (
	dashboardCookieName     = "agenthail_dashboard"
	localLoginLinkLifetime  = 2 * time.Minute
	remoteLoginLinkLifetime = 15 * time.Minute
)

type loginLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (d *Daemon) createLoginLink(remote bool) (loginLink, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return loginLink{}, err
	}
	base, name, lifetime := DashboardBaseURL(config), "Browser on this computer", localLoginLinkLifetime
	if remote {
		status := RemoteAccessStatusForConfig(config)
		if !status.Enabled || status.URL == "" {
			return loginLink{}, errRemoteAccessOff
		}
		base, name, lifetime = status.URL, "Browser through Tailscale", remoteLoginLinkLifetime
	}
	link, err := url.Parse(base)
	if err != nil {
		return loginLink{}, fmt.Errorf("parse dashboard address: %w", err)
	}
	pairing, err := d.Registry.CreateDevicePairing(name, registry.BrowserScopes, lifetime)
	if err != nil {
		return loginLink{}, err
	}
	link.RawQuery = url.Values{"login": {pairing.Secret}}.Encode()
	return loginLink{URL: link.String(), ExpiresAt: pairing.ExpiresAt}, nil
}

var errRemoteAccessOff = fmt.Errorf("remote access is off; turn it on with 'agenthail dashboard remote'")

func (d *Daemon) apiLoginLinksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to create a login link.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Remote bool `json:"remote"`
	}
	if err := decodeAPIV1JSON(w, r, &request, true); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The login link request is invalid.")
		return
	}
	link, err := d.createLoginLink(request.Remote)
	if err == errRemoteAccessOff {
		writeAPIError(w, http.StatusConflict, "remote_access_required", err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "login_link_failed", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, link)
}

// login redeems a login link and gives the browser its own cookie. A browser
// that is already signed in keeps its cookie and leaves the link unused.
func (dashboard *dashboardServer) login(w http.ResponseWriter, r *http.Request, secret string) {
	if !dashboard.authorized(r) {
		_, token, err := dashboard.devices.CompleteBrowserPairing(secret)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "this login link was already used or has expired; run 'agenthail dashboard' for a new one")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: dashboardCookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: dashboardCookieMaxAge})
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (dashboard *dashboardServer) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(dashboardCookieName)
	if err != nil || dashboard.devices == nil {
		return false
	}
	_, err = dashboard.devices.AuthenticateDevice(cookie.Value, "settings")
	return err == nil
}

// browserDevice is the browser device a dashboard cookie signs in.
func (dashboard *dashboardServer) browserDevice(r *http.Request) (registry.Device, bool) {
	cookie, err := r.Cookie(dashboardCookieName)
	if err != nil || dashboard.devices == nil {
		return registry.Device{}, false
	}
	device, err := dashboard.devices.AuthenticateDevice(cookie.Value, "settings")
	return device, err == nil && device.Browser
}

type browserDevices interface {
	CompleteBrowserPairing(secret string) (registry.Device, string, error)
	AuthenticateDevice(token, requiredScope string) (registry.Device, error)
}
