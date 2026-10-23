package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/uiconf"
)

const uiWatchInterval = 2 * time.Second

// uiState owns ui.conf for the daemon: the store, opened on first use, and
// the last revision clients were told about.
type uiState struct {
	dir       string
	mu        sync.Mutex
	store     *uiconf.Store
	err       error
	published string
}

func newUIState(reg *registry.Registry) *uiState {
	return &uiState{dir: filepath.Join(filepath.Dir(uploadsDir(reg)), "ui")}
}

func (u *uiState) open() (*uiconf.Store, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.store == nil && u.err == nil {
		u.store, u.err = uiconf.Open(u.dir)
	}
	return u.store, u.err
}

// uiView is ui.conf as one device should render it. Values are what to draw;
// Chosen is what ui.conf says for the device, and Overrides names the device
// line each overridden key comes from, for settings screens.
type uiView struct {
	Rev       string            `json:"rev"`
	Text      string            `json:"text"`
	Values    map[string]any    `json:"values"`
	Chosen    map[string]string `json:"chosen"`
	Overrides map[string]string `json:"overrides"`
	Problems  []uiconf.Problem  `json:"problems"`
	Change    *uiconf.Entry     `json:"change,omitempty"`
}

func viewFor(state uiconf.State, device uiconf.Device) uiView {
	problems := state.Parsed.Problems
	if problems == nil {
		problems = []uiconf.Problem{}
	}
	chosen, overrides := state.Parsed.Chosen(device)
	return uiView{Rev: state.Rev, Text: state.Text, Values: state.Parsed.Resolve(device), Chosen: chosen, Overrides: overrides, Problems: problems}
}

func requestDevice(r *http.Request) uiconf.Device {
	class := r.URL.Query().Get("class")
	if class == "" {
		class = "web"
	}
	return uiconf.Device{Class: class, Name: r.URL.Query().Get("device")}
}

func (d *Daemon) registerUIAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/ui", func(w http.ResponseWriter, r *http.Request) {
		scope := "read"
		if r.Method == http.MethodPost {
			scope = "settings"
		}
		d.apiV1Guard(dashboard, scope, d.apiUIHandler)(w, r)
	})
	mux.HandleFunc("/api/v1/ui/log", d.apiV1Guard(dashboard, "read", d.apiUILogHandler))
	mux.HandleFunc("/api/v1/ui/presets", d.apiV1Guard(dashboard, "read", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"presets": uiconf.Choices()})
	}))
	mux.HandleFunc("/api/v1/ui/background", func(w http.ResponseWriter, r *http.Request) {
		scope := "read"
		if r.Method == http.MethodPost {
			scope = "settings"
		}
		d.apiV1Guard(dashboard, scope, d.apiUIBackgroundHandler)(w, r)
	})
	mux.HandleFunc("/api/v1/ui/snapshot", d.apiV1Guard(dashboard, "read", d.apiUISnapshotHandler))
	mux.HandleFunc("/api/v1/ui/snapshot/", d.apiV1Guard(dashboard, "read", d.apiUISnapshotUploadHandler))
}

type uiChangeRequest struct {
	Action  string   `json:"action"`
	Set     []string `json:"set"`
	Unset   []string `json:"unset"`
	Text    string   `json:"text"`
	Rev     string   `json:"rev"`
	Message string   `json:"message"`
}

func (d *Daemon) apiUIHandler(w http.ResponseWriter, r *http.Request) {
	store, err := d.ui.open()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "ui_unavailable", err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
		state, err := store.Current()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "ui_read_failed", err.Error())
			return
		}
		d.noteUIRevision(state.Rev)
		writeDashboardJSON(w, http.StatusOK, viewFor(state, requestDevice(r)))
	case http.MethodPost:
		var request uiChangeRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, apiRequestBodyLimit)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The request body must be JSON.")
			return
		}
		state, entry, err := applyUIChange(store, uiconf.Person, request)
		var problems *uiconf.ProblemsError
		if errors.As(err, &problems) {
			writeDashboardJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{"code": "ui_problems", "message": err.Error()}, "problems": problems.Problems})
			return
		}
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "ui_change_refused", err.Error())
			return
		}
		d.noteUIRevision(state.Rev)
		view := viewFor(state, requestDevice(r))
		view.Change = entry
		writeDashboardJSON(w, http.StatusOK, view)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST for this endpoint.")
	}
}

// applyUIChange performs one checked change as author. Assignments are all
// checked before anything is written, so a bad one changes nothing.
func applyUIChange(store *uiconf.Store, author string, request uiChangeRequest) (uiconf.State, *uiconf.Entry, error) {
	message := strings.TrimSpace(request.Message)
	var (
		state   uiconf.State
		changed bool
		err     error
	)
	switch request.Action {
	case "set":
		var assignments []uiconf.Assignment
		for _, line := range request.Set {
			a, err := uiconf.ParseAssignment(line)
			if err != nil {
				return uiconf.State{}, nil, err
			}
			assignments = append(assignments, a)
		}
		if len(assignments) == 0 {
			return uiconf.State{}, nil, errors.New("set needs at least one key = value")
		}
		if message == "" {
			message = "Set " + assignmentNames(assignments)
		}
		state, changed, err = store.Update(author, message, func(text string) string { return uiconf.Set(text, assignments...) })
	case "unset":
		if len(request.Unset) == 0 {
			return uiconf.State{}, nil, errors.New("unset needs at least one key")
		}
		for _, name := range request.Unset {
			key, _, _ := strings.Cut(name, "@")
			if _, ok := uiconf.Lookup(key); !ok {
				return uiconf.State{}, nil, fmt.Errorf("unknown key %s", key)
			}
		}
		if message == "" {
			message = "Reset " + strings.Join(request.Unset, ", ")
		}
		state, changed, err = store.Update(author, message, func(text string) string {
			for _, name := range request.Unset {
				key, device, _ := strings.Cut(name, "@")
				text = uiconf.Unset(text, key, device)
			}
			return text
		})
	case "write":
		if message == "" {
			message = "Replace ui.conf"
		}
		state, changed, err = store.Write(request.Text, author, message)
	case "undo":
		var entry uiconf.Entry
		state, entry, err = store.Undo(request.Rev, author)
		if err != nil {
			return uiconf.State{}, nil, err
		}
		return state, &entry, nil
	default:
		return uiconf.State{}, nil, errors.New("action must be set, unset, write or undo")
	}
	if err != nil || !changed {
		return state, nil, err
	}
	entries, err := store.Log(1)
	if err != nil || len(entries) == 0 {
		return state, nil, err
	}
	return state, &entries[0], nil
}

func assignmentNames(assignments []uiconf.Assignment) string {
	var names []string
	for _, a := range assignments {
		name := a.Key
		if a.Device != "" {
			name += "@" + a.Device
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func (d *Daemon) apiUILogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	store, err := d.ui.open()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "ui_unavailable", err.Error())
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	entries, err := store.Log(limit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "ui_read_failed", err.Error())
		return
	}
	if entries == nil {
		entries = []uiconf.Entry{}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (d *Daemon) apiUIBackgroundHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST for this endpoint.")
		return
	}
	store, err := d.ui.open()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "ui_unavailable", err.Error())
		return
	}
	if r.Method == http.MethodPost {
		name := r.URL.Query().Get("name")
		if err := uiconf.CheckBackgroundUpload(name, r.ContentLength); err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "background_refused", err.Error())
			return
		}
		rel, err := store.ImportBackgroundData(name, r.Body)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "background_refused", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]string{"path": rel})
		return
	}
	asset, err := store.OpenAsset(r.URL.Query().Get("path"))
	if uiconf.IsAssetPath(err) {
		writeAPIError(w, http.StatusBadRequest, "invalid_path", err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "That background is not in ui.conf's folder.")
		return
	}
	defer asset.Close()
	if asset.Tag != "" {
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("ETag", `"`+asset.Tag+`"`)
	} else {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, asset.Name, asset.Modified, asset)
}

// noteUIRevision tells clients when ui.conf reaches a revision they have
// not been told about.
func (d *Daemon) noteUIRevision(rev string) {
	d.ui.mu.Lock()
	changed := rev != "" && rev != d.ui.published
	previous := d.ui.published
	d.ui.published = rev
	d.ui.mu.Unlock()
	if changed && previous != "" {
		d.publishEvent("ui.changed", "", map[string]string{"rev": rev})
	}
}

// watchUI commits hand edits and announces every new revision, whether it
// came from an editor, the CLI or the apps.
func (d *Daemon) watchUI(ctx context.Context) {
	store, err := d.ui.open()
	if err != nil {
		d.log.Printf("ui.conf: %s", err)
		return
	}
	wake := filewake.Follow(store.Path())
	defer wake.Release()
	ticker := time.NewTicker(uiWatchInterval)
	defer ticker.Stop()
	signature := ""
	for {
		if next := uiSignature(store); next != signature {
			signature = next
			if state, err := store.Current(); err != nil {
				d.log.Printf("ui.conf: %s", err)
			} else {
				d.noteUIRevision(state.Rev)
			}
			signature = uiSignature(store)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake.C():
		case <-ticker.C:
		}
	}
}

func uiSignature(store *uiconf.Store) string {
	var parts []string
	for _, path := range []string{store.Path(), filepath.Join(store.Dir(), ".git", "logs", "HEAD")} {
		info, err := os.Stat(path)
		if err != nil {
			parts = append(parts, "-")
			continue
		}
		parts = append(parts, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()))
	}
	return strings.Join(parts, "|")
}
