package daemon

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/nodehost"
	"github.com/zm2231/agenthail/internal/proxyacct"
)

//go:embed extensions_sdk.js
var extensionSDK []byte

const (
	extensionActionBodyLimit = 1 << 20
	extensionPageLimit       = 8 << 20
	extensionPageCSP         = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"
)

func (d *Daemon) registerExtensionRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/extensions", func(w http.ResponseWriter, r *http.Request) {
		scope := "read"
		if r.Method != http.MethodGet {
			scope = "settings"
		}
		d.apiV1Guard(dashboard, scope, d.apiExtensionsHandler)(w, r)
	})
	mux.HandleFunc("GET /api/v1/extensions/history", d.apiV1Guard(dashboard, "read", d.apiExtensionHistoryHandler))
	mux.HandleFunc("POST /api/v1/extensions/build", d.apiV1Guard(dashboard, "control", d.apiExtensionBuildHandler))
	mux.HandleFunc("POST /api/v1/extensions/{id}/call", d.apiV1Guard(dashboard, "control", d.apiExtensionCallHandler))
	mux.HandleFunc("GET /x/_ah.js", d.apiV1Guard(dashboard, "read", func(w http.ResponseWriter, r *http.Request) {
		extensionPageHeaders(w)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(extensionSDK)
	}))
	mux.HandleFunc("POST /x/{id}/a/{action}", d.apiV1Guard(dashboard, "control", d.extensionActionHandler))
	mux.HandleFunc("GET /x/{id}/{path...}", d.apiV1Guard(dashboard, "read", d.extensionPageHandler))
	mux.HandleFunc("GET /x/{id}", d.apiV1Guard(dashboard, "read", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/x/"+url.PathEscape(r.PathValue("id"))+"/", http.StatusFound)
	}))
}

func extensionPageHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", extensionPageCSP)
}

func (d *Daemon) extensionPageHandler(w http.ResponseWriter, r *http.Request) {
	loaded, ok := d.extensions.find(r.PathValue("id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "No extension "+r.PathValue("id")+".")
		return
	}
	file, ok := loaded.ext.PagePath(r.PathValue("path"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "The extension serves its page and files under assets/ only.")
		return
	}
	info, err := os.Stat(file)
	if err != nil || info.Size() > extensionPageLimit {
		writeAPIError(w, http.StatusNotFound, "not_found", "That file is missing or too large to serve.")
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "That file could not be read.")
		return
	}
	kind := mime.TypeByExtension(filepath.Ext(file))
	if kind == "" {
		kind = http.DetectContentType(data)
	}
	extensionPageHeaders(w)
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(file), info.ModTime(), bytes.NewReader(data))
}

type extensionCallerInfo struct {
	Kind  string `json:"kind"`
	Actor string `json:"actor"`
}

func (d *Daemon) extensionActionHandler(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	loaded, ok := d.extensions.find(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "No extension "+id+".")
		return
	}
	if !slices.Contains(loaded.host.Actions, action) {
		writeAPIError(w, http.StatusNotFound, "not_found", "Extension "+id+" has no action "+action+".")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, extensionActionBodyLimit))
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "The action input is larger than 1 MiB.")
		return
	}
	input := json.RawMessage("{}")
	if len(bytes.TrimSpace(body)) > 0 {
		if !json.Valid(body) {
			writeAPIError(w, http.StatusBadRequest, "invalid", "The action input must be JSON.")
			return
		}
		input = body
	}
	p := principalFromRequest(r)
	ctx, cancel := context.WithTimeout(r.Context(), extensionActionLimit)
	defer cancel()
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	err = d.extensions.host.Client().Call(ctx, "action", map[string]any{"id": id, "name": action, "input": input, "caller": extensionCallerInfo{Kind: string(p.Kind), Actor: p.actor()}}, &reply)
	if err != nil {
		writeExtensionError(w, err)
		return
	}
	if len(reply.Result) == 0 {
		reply.Result = json.RawMessage("null")
	}
	writeDashboardJSON(w, http.StatusOK, map[string]json.RawMessage{"result": reply.Result})
}

func writeExtensionError(w http.ResponseWriter, err error) {
	var hostErr *nodehost.Error
	var callErr *extensionCallError
	switch {
	case errors.As(err, &callErr):
		status := map[string]int{"not_found": http.StatusNotFound, "invalid": http.StatusBadRequest, "conflict": http.StatusConflict}[callErr.code]
		writeAPIError(w, status, callErr.code, callErr.message)
	case errors.Is(err, nodehost.ErrNotRunning):
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", errExtensionHostDown.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(w, http.StatusGatewayTimeout, "timed_out", "The extension did not answer within 30 seconds.")
	case errors.As(err, &hostErr):
		status := http.StatusBadGateway
		switch hostErr.Code {
		case "not_found":
			status = http.StatusNotFound
		case "invalid":
			status = http.StatusBadRequest
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
		writeAPIError(w, status, hostErr.Code, hostErr.Message)
	default:
		writeAPIError(w, http.StatusBadGateway, "failed", err.Error())
	}
}

func (d *Daemon) apiExtensionsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeDashboardJSON(w, http.StatusOK, d.extensionsView())
	case http.MethodPost:
		var request struct {
			Action    string            `json:"action"`
			ID        string            `json:"id"`
			Mount     string            `json:"mount"`
			Key       *proxyacct.KeyRef `json:"key"`
			Rev       string            `json:"rev"`
			SessionID string            `json:"sessionId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid", "Send {\"action\": \"reload\"}, {\"action\": \"approve\", \"id\": \"<extension>\"} or {\"action\": \"key\", \"id\": \"<extension>\", \"mount\": \"<mount>\", \"key\": {...}}.")
			return
		}
		if (request.Action == "approve" || request.Action == "key") && strings.TrimSpace(request.SessionID) != "" {
			writeActionResult(w, actionResult{}, d.askPersonToGrant(request.SessionID, request.Action, request.ID, request.Mount, request.Key))
			return
		}
		switch request.Action {
		case "undo":
			undone, err := d.undoExtensionChange(r.Context(), request.Rev)
			if err != nil {
				writeActionResult(w, actionResult{}, err)
				return
			}
			writeDashboardJSON(w, http.StatusOK, map[string]any{"undone": undone.Reverted, "removed": undone.Removed})
			return
		case "reload", "mounts":
		case "approve":
			if err := d.approveExtension(request.ID); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid", err.Error())
				return
			}
		case "key":
			if err := d.setExtensionMountKey(request.ID, request.Mount, request.Key); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid", err.Error())
				return
			}
		default:
			writeAPIError(w, http.StatusBadRequest, "invalid", "Unknown action "+request.Action+"; use reload, approve, key or undo.")
			return
		}
		if err := d.syncExtensionMounts(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if request.Action != "key" && request.Action != "mounts" {
			if err := d.reloadExtensions(r.Context()); err != nil {
				writeAPIError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
				return
			}
		}
		writeDashboardJSON(w, http.StatusOK, d.extensionsView())
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST.")
	}
}

// onDisk reads the extension's manifest as it is now, so the person approves
// what they can read, not a cached copy.
func (d *Daemon) onDisk(id string) (extension.Extension, error) {
	if d.extensions == nil {
		return extension.Extension{}, errExtensionsOff
	}
	if !extension.ValidID(id) {
		return extension.Extension{}, errors.New("no extension " + id)
	}
	found, err := extension.Discover(extension.Workspace(d.extensions.home))
	if err != nil {
		return extension.Extension{}, err
	}
	for _, ext := range found {
		if ext.ID != id {
			continue
		}
		if ext.Err != nil {
			return extension.Extension{}, errors.New("extension " + id + " is not valid: " + ext.Err.Error())
		}
		return ext, nil
	}
	return extension.Extension{}, errors.New("no extension " + id + " in " + strings.TrimSuffix(extension.Workspace(d.extensions.home), "/"))
}

func (d *Daemon) approveExtension(id string) error {
	ext, err := d.onDisk(id)
	if err != nil {
		return err
	}
	return d.extensions.approvals.Grant(id, ext.Manifest)
}

func (d *Daemon) setExtensionMountKey(id, name string, key *proxyacct.KeyRef) error {
	ext, err := d.onDisk(id)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(ext.Manifest.Mounts, func(mount extension.Mount) bool { return mount.Name == name })
	if index < 0 {
		return errors.New("extension " + id + " declares no mount " + name)
	}
	if key != nil {
		if err := key.Validate(); err != nil {
			return errors.New("the key " + err.Error())
		}
	}
	return d.extensions.approvals.SetKey(id, ext.Manifest.Mounts[index], key)
}

func (d *Daemon) apiExtensionHistoryHandler(w http.ResponseWriter, r *http.Request) {
	history, err := d.extensionHistory()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	changes := []extension.Change{}
	if history != nil {
		limit := 50
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		if changes, err = history.Log(limit, "HEAD"); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"changes": changes})
}
