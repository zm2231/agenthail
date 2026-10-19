package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type signinConsumerView struct {
	signin.ConsumerInfo
	ProfileID *string `json:"profileId"`
}

type signinDetectedProfileView struct {
	signin.DetectedProfile
	ProfileID string `json:"profileId,omitempty"`
}

type signinBrowserView struct {
	signin.DetectedBrowser
	Profiles []signinDetectedProfileView `json:"profiles"`
}

type signinView struct {
	Profiles  []signin.Profile     `json:"profiles"`
	Consumers []signinConsumerView `json:"consumers"`
	Browsers  []signinBrowserView  `json:"browsers"`
}

func (d *Daemon) registerSigninAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/signin", d.apiV1Guard(dashboard, "settings", d.apiSigninHandler))
	mux.HandleFunc("/api/v1/signin/profiles", d.apiV1Guard(dashboard, "settings", d.apiSigninProfilesHandler))
	mux.HandleFunc("/api/v1/signin/bindings", d.apiV1Guard(dashboard, "settings", d.apiSigninBindingsHandler))
}

func (d *Daemon) signinView(r *http.Request) (signinView, error) {
	profiles, err := d.Registry.ListSigninProfiles()
	if err != nil {
		return signinView{}, err
	}
	bindings, err := d.Registry.SigninBindings()
	if err != nil {
		return signinView{}, err
	}
	view := signinView{Profiles: profiles, Consumers: []signinConsumerView{}, Browsers: []signinBrowserView{}}
	for _, consumer := range signin.Consumers {
		entry := signinConsumerView{ConsumerInfo: consumer}
		if id, ok := bindings[consumer.ID]; ok {
			entry.ProfileID = &id
		}
		view.Consumers = append(view.Consumers, entry)
	}
	added := map[string]string{}
	for _, profile := range profiles {
		added[string(profile.Browser)+"\x00"+profile.Profile] = profile.ID
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return signinView{}, err
	}
	for _, browser := range signin.Detect(r.Context(), home) {
		entry := signinBrowserView{DetectedBrowser: browser, Profiles: []signinDetectedProfileView{}}
		for _, profile := range browser.Profiles {
			entry.Profiles = append(entry.Profiles, signinDetectedProfileView{DetectedProfile: profile, ProfileID: added[string(browser.Browser)+"\x00"+profile.Profile]})
		}
		view.Browsers = append(view.Browsers, entry)
	}
	return view, nil
}

func (d *Daemon) apiSigninHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	view, err := d.signinView(r)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "signin_read_failed", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, view)
}

func (d *Daemon) apiSigninProfilesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var request struct {
			Browser string `json:"browser"`
			Profile string `json:"profile"`
			Label   string `json:"label"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "A browser and a browser profile are required.")
			return
		}
		browser := signin.Browser(strings.ToLower(strings.TrimSpace(request.Browser)))
		if !browser.Valid() {
			writeAPIError(w, http.StatusBadRequest, "invalid_browser", fmt.Sprintf("Unsupported browser %q. Choose one of: %s.", request.Browser, browserNames()))
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "signin_read_failed", err.Error())
			return
		}
		detected, profile, found := signin.Find(signin.Detect(r.Context(), home), browser, request.Profile)
		if !found {
			message := fmt.Sprintf("%s profile %q was not found on this computer. Run `agenthail signin list` to see the profiles that exist.", browser.Label(), request.Profile)
			if detected.Browser == "" {
				message = fmt.Sprintf("%s was not found on this computer.", browser.Label())
			} else if !detected.Available && detected.Detail != "" {
				message = fmt.Sprintf("%s is unavailable: %s.", browser.Label(), detected.Detail)
			}
			writeAPIError(w, http.StatusBadRequest, "browser_profile_not_found", message)
			return
		}
		label := strings.TrimSpace(request.Label)
		if label == "" {
			label = browser.Label() + " " + profile.Name
		}
		record, err := d.Registry.AddSigninProfile(label, browser, profile.Profile)
		if err != nil {
			writeAPIError(w, http.StatusConflict, "signin_profile_conflict", err.Error())
			return
		}
		d.publishEvent("signin.changed", record.ID, map[string]any{"profileId": record.ID})
		writeDashboardJSON(w, http.StatusCreated, map[string]any{"profile": record})
	case http.MethodDelete:
		ref := strings.TrimSpace(r.URL.Query().Get("id"))
		if ref == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "Pass the profile ID or label as ?id=.")
			return
		}
		record, err := d.Registry.RemoveSigninProfile(ref)
		if errors.Is(err, registry.ErrSigninProfileNotFound) {
			writeAPIError(w, http.StatusNotFound, "signin_profile_not_found", err.Error())
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "signin_remove_failed", err.Error())
			return
		}
		d.publishEvent("signin.changed", record.ID, map[string]any{"profileId": record.ID})
		writeDashboardJSON(w, http.StatusOK, map[string]any{"profile": record})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST or DELETE for this endpoint.")
	}
}

func (d *Daemon) apiSigninBindingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use PUT for this endpoint.")
		return
	}
	var request struct {
		Consumer  string          `json:"consumer"`
		ProfileID json.RawMessage `json:"profileId"`
	}
	const required = "A consumer and a profileId (or null to clear it) are required."
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil || len(request.ProfileID) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", required)
		return
	}
	consumer, ok := signin.LookupConsumer(request.Consumer)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_consumer", fmt.Sprintf("Unknown consumer %q. Choose one of: %s.", request.Consumer, consumerNames()))
		return
	}
	ref := ""
	if string(request.ProfileID) != "null" {
		if err := json.Unmarshal(request.ProfileID, &ref); err != nil || strings.TrimSpace(ref) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", required)
			return
		}
		ref = strings.TrimSpace(ref)
	}
	profile, err := d.Registry.BindSignin(consumer.ID, ref)
	if errors.Is(err, registry.ErrSigninProfileNotFound) {
		writeAPIError(w, http.StatusNotFound, "signin_profile_not_found", err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "signin_bind_failed", err.Error())
		return
	}
	d.publishEvent("signin.changed", string(consumer.ID), map[string]any{"consumer": consumer.ID})
	go d.refreshSigninSurfaces()
	writeDashboardJSON(w, http.StatusOK, map[string]any{"consumer": consumer.ID, "profile": profile})
}

// refreshSigninSurfaces re-checks the surfaces that read cookies so a new
// binding shows in health without waiting for the next discovery pass.
func (d *Daemon) refreshSigninSurfaces() {
	for _, kind := range []surface.SurfaceKind{surface.KindNotion, surface.KindClaude} {
		adapter := d.surfaceForKind(kind)
		if adapter == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := adapter.List(ctx)
		d.publishSurfaceHealth(d.recordSurfaceHealth(ctx, adapter, err), time.Now().UTC())
		cancel()
	}
}

func browserNames() string {
	names := make([]string, len(signin.Browsers))
	for i, browser := range signin.Browsers {
		names[i] = string(browser)
	}
	return strings.Join(names, ", ")
}

func consumerNames() string {
	names := make([]string, len(signin.Consumers))
	for i, consumer := range signin.Consumers {
		names[i] = string(consumer.ID)
	}
	return strings.Join(names, ", ")
}
