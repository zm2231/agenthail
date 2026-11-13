package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/extension"
)

const extensionCallsKept = 20

// An extensionCall is one run of an action or a job through call or run,
// with the hash of the folder whose code ran. Only these records, never an
// agent's own words, mark a change as checked.
type extensionCall struct {
	Kind   string    `json:"kind"`
	Name   string    `json:"name"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
	Tree   string    `json:"tree,omitempty"`
	Caller string    `json:"caller"`
}

type extensionCalls struct {
	path string
	mu   sync.Mutex
}

func (c *extensionCalls) read() (map[string][]extensionCall, error) {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]extensionCall{}, nil
	}
	if err != nil {
		return nil, err
	}
	calls := map[string][]extensionCall{}
	return calls, json.Unmarshal(data, &calls)
}

func (c *extensionCalls) record(id string, call extensionCall) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls, err := c.read()
	if err != nil {
		return err
	}
	list := append([]extensionCall{call}, calls[id]...)
	calls[id] = list[:min(len(list), extensionCallsKept)]
	data, err := json.MarshalIndent(calls, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(c.path, append(data, '\n'), 0o600)
}

// verified is the newest successful call that ran the folder as tree holds it.
func (c *extensionCalls) verified(id, tree string) (extensionCall, bool) {
	if tree == "" {
		return extensionCall{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	calls, err := c.read()
	if err != nil {
		return extensionCall{}, false
	}
	for _, call := range calls[id] {
		if call.OK && call.Tree == tree {
			return call, true
		}
	}
	return extensionCall{}, false
}

func (c *extensionCalls) forget(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls, err := c.read()
	if err != nil || calls[id] == nil {
		return err
	}
	delete(calls, id)
	data, err := json.MarshalIndent(calls, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(c.path, append(data, '\n'), 0o600)
}

func (d *Daemon) extensionTrees() map[string]string {
	history, err := d.extensionHistory()
	if err != nil || history == nil {
		return nil
	}
	trees, err := history.FolderTrees()
	if err != nil {
		d.log.Printf("extensions: hash the folders: %s", err)
		return nil
	}
	return trees
}

type extensionCallRequest struct {
	Action    string          `json:"action"`
	Job       string          `json:"job"`
	Input     json.RawMessage `json:"input"`
	SessionID string          `json:"sessionId"`
}

func (d *Daemon) apiExtensionCallHandler(w http.ResponseWriter, r *http.Request) {
	var request extensionCallRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, extensionActionBodyLimit+4096)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid", "Send {\"action\": \"<name>\", \"input\": {...}} or {\"job\": \"<name>\"}.")
		return
	}
	if (request.Action == "") == (request.Job == "") {
		writeAPIError(w, http.StatusBadRequest, "invalid", "Name either an action or a job.")
		return
	}
	caller, err := d.extensionCallCaller(r, request.SessionID)
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	result, err := d.callExtension(r.Context(), r.PathValue("id"), request, caller)
	if err != nil {
		writeExtensionError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}

// A session named by this machine's CLI runs the call as that session, the
// way it would reach any other tool; anyone else is who their credential says.
func (d *Daemon) extensionCallCaller(r *http.Request, sessionID string) (extensionCallerInfo, error) {
	if strings.TrimSpace(sessionID) == "" {
		p := principalFromRequest(r)
		return extensionCallerInfo{Kind: string(p.Kind), Actor: p.actor()}, nil
	}
	if !localReach(r) {
		return extensionCallerInfo{}, actionFailed(http.StatusForbidden, "remote_refused", "A session is named only by this machine's CLI.")
	}
	session, err := d.Registry.Session(strings.TrimSpace(sessionID))
	if err != nil {
		return extensionCallerInfo{}, actionFailed(http.StatusNotFound, "not_found", "Agenthail does not know the calling session "+sessionID+".")
	}
	return extensionCallerInfo{Kind: string(principalSession), Actor: "session " + d.resolveDisplay(session.ID)}, nil
}

type extensionCallResult struct {
	Result   json.RawMessage `json:"result,omitempty"`
	Ran      string          `json:"ran"`
	Tree     string          `json:"tree,omitempty"`
	Reloaded bool            `json:"reloaded,omitempty"`
}

// callExtension first loads the folder as it is on disk, so what runs is the
// version a later commit records, and then records the outcome.
func (d *Daemon) callExtension(ctx context.Context, id string, request extensionCallRequest, caller extensionCallerInfo) (extensionCallResult, error) {
	if d.extensions == nil {
		return extensionCallResult{}, errExtensionsOff
	}
	if !extension.ValidID(id) {
		return extensionCallResult{}, extensionNotFound("No extension " + id + ".")
	}
	out := extensionCallResult{}
	if disk := d.extensionTrees()[id]; disk != "" {
		loaded, ok := d.extensions.find(id)
		if !ok || loaded.tree != disk {
			if err := d.syncExtensionMounts(); err != nil {
				d.log.Printf("extensions: sync mounts before a call: %s", err)
			}
			if err := d.reloadExtensions(ctx); err != nil {
				return out, err
			}
			out.Reloaded = true
		}
	}
	loaded, ok := d.extensions.find(id)
	if !ok {
		return out, extensionNotFound("No extension " + id + ".")
	}
	if loaded.host.State != "loaded" {
		return out, extensionInvalid(fmt.Sprintf("Extension %s is %s: %s", id, loaded.host.State, loaded.host.Error))
	}
	out.Tree = loaded.tree
	call := extensionCall{Kind: "action", Name: request.Action, Tree: loaded.tree, Caller: caller.Actor}
	var err error
	if request.Job != "" {
		call.Kind, call.Name = "job", request.Job
		err = d.runExtensionJobNow(ctx, id, loaded, request.Job)
	} else {
		out.Result, err = d.runExtensionAction(ctx, id, loaded, request.Action, request.Input, caller)
	}
	call.At, call.OK = time.Now(), err == nil
	if err != nil {
		call.Error = err.Error()
	}
	out.Ran = call.Kind + " " + call.Name
	if recordErr := d.extensions.calls.record(id, call); recordErr != nil {
		d.log.Printf("extensions: record a call to %s: %s", id, recordErr)
	} else if call.OK {
		d.rewordExtensionChangeAsks()
	}
	return out, err
}

func (d *Daemon) runExtensionAction(ctx context.Context, id string, loaded *loadedExtension, action string, input json.RawMessage, caller extensionCallerInfo) (json.RawMessage, error) {
	if !slices.Contains(loaded.host.Actions, action) {
		return nil, extensionNotFound("Extension " + id + " has no action " + action + ".")
	}
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage("{}")
	}
	ctx, cancel := context.WithTimeout(ctx, extensionActionLimit)
	defer cancel()
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	if err := d.extensions.host.Client().Call(ctx, "action", map[string]any{"id": id, "name": action, "input": input, "caller": caller}, &reply); err != nil {
		return nil, err
	}
	if len(reply.Result) == 0 {
		reply.Result = json.RawMessage("null")
	}
	return reply.Result, nil
}

// A job run by hand takes the scheduler's place: never two at once, and the
// next scheduled run counts from its end.
func (d *Daemon) runExtensionJobNow(ctx context.Context, id string, loaded *loadedExtension, name string) error {
	index := slices.IndexFunc(loaded.host.Jobs, func(job extensionJob) bool { return job.Name == name })
	if index < 0 {
		return extensionNotFound("Extension " + id + " has no job " + name + ".")
	}
	r := d.extensions
	r.mu.Lock()
	state := loaded.jobs[name]
	if state == nil || state.running {
		r.mu.Unlock()
		return extensionBusy("Job " + name + " is running now; run it again once it ends.")
	}
	state.running = true
	r.mu.Unlock()
	return d.runExtensionJob(context.WithoutCancel(ctx), id, loaded.host.Jobs[index], state)
}

func extensionNotFound(message string) error {
	return &extensionCallError{code: "not_found", message: message}
}

func extensionInvalid(message string) error {
	return &extensionCallError{code: "invalid", message: message}
}

func extensionBusy(message string) error {
	return &extensionCallError{code: "conflict", message: message}
}

type extensionCallError struct{ code, message string }

func (e *extensionCallError) Error() string { return e.message }

// extensionChecks says, for each extension a change left in place, whether a
// successful call ran exactly the version that change recorded.
func (d *Daemon) extensionChecks(history *extension.History, change extension.Change) []string {
	var lines []string
	for _, files := range change.Extensions {
		if files.Removed {
			continue
		}
		tree, err := history.CommittedTree(change.Rev, files.ID)
		if err != nil || tree == "" {
			continue
		}
		if call, ok := d.extensions.calls.verified(files.ID, tree); ok {
			lines = append(lines, fmt.Sprintf("%s verified: %s ran ok at %s against this version", files.ID, call.Name, call.At.Local().Format("15:04")))
		} else {
			lines = append(lines, files.ID+" not verified")
		}
		if waits := d.extensionWaitsFor(files.ID); waits != "" {
			lines = append(lines, files.ID+" needs your approval: "+waits)
		}
	}
	return lines
}

func (d *Daemon) extensionWaitsFor(id string) string {
	ext, err := d.onDisk(id)
	if err != nil {
		return ""
	}
	granted, err := d.extensions.approvals.Granted(id)
	if err != nil {
		return ""
	}
	_, waits := extension.Effective(ext.Manifest.Capabilities, granted.Capabilities)
	for _, mount := range ext.Manifest.Mounts {
		if _, ok := granted.MountApproved(mount); !ok {
			waits = append(waits, "mount "+mount.Name+" ("+mount.Upstream+")")
		}
	}
	return strings.Join(waits, ", ")
}
