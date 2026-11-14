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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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
	mux.HandleFunc("GET /api/v1/extensions/history/{rev}", d.apiV1Guard(dashboard, "read", d.apiExtensionChangeHandler))
	mux.HandleFunc("POST /api/v1/extensions/build", d.apiV1Guard(dashboard, "control", d.apiExtensionBuildHandler))
	mux.HandleFunc("POST /api/v1/extensions/{id}/call", d.apiV1Guard(dashboard, "control", d.apiExtensionCallHandler))
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
		case "keep":
			if d.extensions == nil {
				writeAPIError(w, http.StatusServiceUnavailable, "unavailable", errExtensionsOff.Error())
				return
			}
			if err := d.keepExtensionChange(request.Rev, principalFromRequest(r)); err != nil {
				writeActionResult(w, actionResult{}, err)
				return
			}
			writeDashboardJSON(w, http.StatusOK, map[string]any{"kept": request.Rev})
			return
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
	var ids []string
	if id := r.URL.Query().Get("id"); id != "" {
		if !extension.ValidID(id) {
			writeAPIError(w, http.StatusBadRequest, "invalid", "Not an extension id: "+id+".")
			return
		}
		ids = append(ids, id)
	}
	changes := []extension.Change{}
	if history != nil {
		limit := 50
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 500 {
			limit = n
		}
		if changes, err = history.Log(limit, "HEAD", ids...); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}
	pending := d.pendingExtensionRevs()
	listed := make([]extensionChangeView, 0, len(changes))
	for _, change := range changes {
		listed = append(listed, extensionChangeView{Change: change, Pending: pending[change.Rev]})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"changes": listed})
}

type extensionChangeView struct {
	extension.Change
	Pending bool `json:"pending,omitempty"`
}

const extensionPatchLimit = 256 << 10

func (d *Daemon) apiExtensionChangeHandler(w http.ResponseWriter, r *http.Request) {
	history, err := d.extensionHistory()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	if history == nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "There is no extensions folder.")
		return
	}
	rev := r.PathValue("rev")
	changes, err := history.Log(1, rev)
	if err != nil || len(changes) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_found", "Unknown change "+rev+".")
		return
	}
	patch, truncated, err := history.Patch(changes[0].Rev, extensionPatchLimit)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"change": extensionChangeView{Change: changes[0], Pending: d.pendingExtensionRevs()[changes[0].Rev]}, "patch": patch, "truncated": truncated})
}

func (d *Daemon) extensionSourcesHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	loaded, ok := d.extensions.snapshot(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found", "No extension "+id+".")
		return
	}
	if loaded.host.State != "loaded" {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "Extension "+id+" is "+loaded.host.State+"; its sources come back once it loads.")
		return
	}
	params := map[string]any{"id": id}
	if name := r.URL.Query().Get("name"); name != "" {
		params["name"] = name
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var reply json.RawMessage
	if err := d.extensions.host.Client().Call(ctx, "sources", params, &reply); err != nil {
		if hostErr, ok := errors.AsType[*nodehost.Error](err); ok && hostErr.Code == "not_found" {
			writeAPIError(w, http.StatusNotFound, "not_found", hostErr.Message)
			return
		}
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "The extension host did not answer: "+err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, http.StatusOK, reply)
}
