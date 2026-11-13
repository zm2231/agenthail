package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/nodehost"
)

const (
	extensionHostProtocol = 1
	extensionActionLimit  = 30 * time.Second
	extensionJobLimit     = 10 * time.Minute
	extensionEventBatch   = 200
)

var extensionJobTick = time.Second

type extensionJob struct {
	Name         string `json:"name"`
	EverySeconds int    `json:"everySeconds"`
	Source       bool   `json:"source,omitempty"`
}

type extensionHostSummary struct {
	ID             string         `json:"id"`
	State          string         `json:"state"`
	Error          string         `json:"error,omitempty"`
	Actions        []string       `json:"actions"`
	Jobs           []extensionJob `json:"jobs"`
	Events         bool           `json:"events"`
	LastEventError string         `json:"lastEventError,omitempty"`
}

type extensionJobState struct {
	LastRun   time.Time `json:"lastRun,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	running   bool
	due       time.Time
	every     time.Duration
}

type loadedExtension struct {
	ext     extension.Extension
	held    []string
	pending []string
	token   string
	host    extensionHostSummary
	jobs    map[string]*extensionJobState
	// tree is the folder's hash while this load read it, or "" when it
	// changed during the load, so a call is recorded only against a version
	// known to be the one that ran.
	tree string
}

type extensionRuntime struct {
	home      string
	approvals *extension.Approvals
	host      *nodehost.Supervisor
	mounts    *extensionMounts
	changes   *extensionChanges
	calls     *extensionCalls
	grants    *extensionGrants

	loading   sync.Mutex
	mu        sync.Mutex
	loaded    map[string]*loadedExtension
	tokens    map[string]string
	hostErr   string
	loggedErr string
	up        bool
	reloading bool
	// generation counts loads; a run that saw it change may have run other code.
	generation uint64
}

func (d *Daemon) SetExtensionHost(home string, launch func() (nodehost.Launch, error)) {
	runtime := &extensionRuntime{home: home, approvals: extension.NewApprovals(home), mounts: newExtensionMounts(home), changes: newExtensionChanges(home), calls: &extensionCalls{path: filepath.Join(extension.DataDir(home), "calls.json")}, grants: &extensionGrants{path: filepath.Join(extension.DataDir(home), "grants.json")}, loaded: map[string]*loadedExtension{}, tokens: map[string]string{}}
	runtime.host = &nodehost.Supervisor{
		Name:     "extension host",
		StateDir: extensionHostDir(home),
		Socket:   filepath.Join(home, ".agenthail", "extension-host.sock"),
		Protocol: extensionHostProtocol,
		Launch:   launch,
		Args:     func() []string { return []string{"--store", extension.StorePath(home)} },
		Ready: func(ctx context.Context, _ nodehost.Client) error {
			return d.loadExtensions(ctx)
		},
		Down: func(err error) {
			runtime.mu.Lock()
			runtime.up = false
			runtime.hostErr = err.Error()
			failure := err.Error()
			if hostErr, ok := errors.AsType[*nodehost.HostError](err); ok {
				failure = hostErr.Cause.Error()
			}
			fresh := failure != runtime.loggedErr
			runtime.loggedErr = failure
			runtime.mu.Unlock()
			if fresh {
				d.log.Printf("extension host: %s", err)
			}
			d.invalidateDashboard()
		},
	}
	d.extensions = runtime
}

func extensionHostDir(home string) string { return extension.DataDir(home) + "/host" }

func (d *Daemon) superviseExtensions(ctx context.Context) {
	if d.extensions == nil {
		return
	}
	if err := d.seedExtensionWorkspace(true); err != nil {
		d.logExtensionHistory(err)
	}
	turns := make(chan struct{})
	go func() {
		defer close(turns)
		d.runExtensionTurns(ctx)
	}()
	go d.runExtensionJobs(ctx)
	go d.runExtensionMounts(ctx)
	go d.pumpExtensionEvents(ctx)
	d.extensions.host.Run(ctx)
	<-turns
}

func extensionToken() string { return "ext_" + rand.Text() + rand.Text() }

// loadExtensions reads the workspace again and has the host load every valid
// extension with fresh credentials; the previous credentials stop working.
// One load runs at a time, so the host's extensions and the daemon's tokens
// always come from the same load. Events wait until a load succeeds, since
// until then the host may hold no handlers; a failed load is followed by a
// host restart, whose own load ends the wait.
func (d *Daemon) loadExtensions(ctx context.Context) error {
	r := d.extensions
	r.loading.Lock()
	defer r.loading.Unlock()
	trees := d.extensionTrees()
	found, err := extension.Discover(extension.Workspace(r.home))
	if err != nil {
		return err
	}
	mcpURL, fetchURL := "", ""
	if credentials := d.mcpCredentials.Load(); credentials != nil {
		mcpURL = credentials.URL()
		fetchURL = strings.TrimSuffix(mcpURL, "/mcp") + extensionFetchPath
	}
	next := map[string]*loadedExtension{}
	tokens := map[string]string{}
	type spec struct {
		ID       string `json:"id"`
		Dir      string `json:"dir"`
		Token    string `json:"token"`
		MCPURL   string `json:"mcpURL"`
		FetchURL string `json:"fetchURL"`
	}
	// The host iterates this list, so a workspace with no extensions must send
	// [] and not null.
	specs := []spec{}
	for _, ext := range found {
		loaded := &loadedExtension{ext: ext, jobs: map[string]*extensionJobState{}, tree: trees[ext.ID]}
		next[ext.ID] = loaded
		if ext.Err != nil {
			loaded.host = extensionHostSummary{ID: ext.ID, State: "invalid", Error: ext.Err.Error()}
			continue
		}
		granted, err := r.approvals.Granted(ext.ID)
		if err != nil {
			return fmt.Errorf("read extension approvals: %w", err)
		}
		loaded.held, loaded.pending = extension.Effective(ext.Manifest.Capabilities, granted.Capabilities)
		if _, err := d.Registry.EnsureExtensionSender(ext.ID, ext.Manifest.Label); err != nil {
			return fmt.Errorf("record extension %s as a sender: %w", ext.ID, err)
		}
		loaded.token = extensionToken()
		tokens[loaded.token] = ext.ID
		specs = append(specs, spec{ID: ext.ID, Dir: ext.Dir, Token: loaded.token, MCPURL: mcpURL, FetchURL: fetchURL})
	}
	var status struct {
		Extensions []extensionHostSummary `json:"extensions"`
	}
	r.mu.Lock()
	previous := r.loaded
	r.loaded = next
	r.tokens = tokens
	r.reloading = true
	r.generation++
	r.mu.Unlock()
	err = r.host.Client().Call(ctx, "load", map[string]any{"extensions": specs, "writeTools": extensionWriteTools()}, &status)
	now := time.Now()
	if err != nil {
		return fmt.Errorf("%w: %w", errExtensionLoadFailed, err)
	}
	after := d.extensionTrees()
	r.mu.Lock()
	r.reloading = false
	for _, summary := range status.Extensions {
		loaded, ok := next[summary.ID]
		if !ok {
			continue
		}
		loaded.host = summary
		if after[summary.ID] != loaded.tree {
			loaded.tree = ""
		}
		for _, job := range summary.Jobs {
			loaded.jobs[job.Name] = carriedJobState(previous[summary.ID], job, now)
		}
	}
	r.up = true
	r.hostErr = ""
	recovered := r.loggedErr != ""
	r.loggedErr = ""
	r.mu.Unlock()
	if recovered {
		d.log.Printf("extension host: loaded again")
	}
	d.invalidateDashboard()
	return nil
}

// A job keeps its state object across loads, so a run still going when the
// extension reloads holds off the next one until it finishes. A source runs
// again at once, since the code that collects it may have changed.
func carriedJobState(previous *loadedExtension, job extensionJob, now time.Time) *extensionJobState {
	every := time.Duration(job.EverySeconds) * time.Second
	if previous != nil {
		if kept, ok := previous.jobs[job.Name]; ok {
			kept.every = every
			switch {
			case kept.running:
			case job.Source:
				kept.due = now
			case !kept.LastRun.IsZero():
				kept.due = kept.LastRun.Add(every)
			}
			return kept
		}
	}
	return &extensionJobState{due: now, every: every}
}

func (d *Daemon) reloadExtensions(ctx context.Context) error {
	if d.extensions == nil {
		return errExtensionsOff
	}
	d.extensions.mu.Lock()
	up := d.extensions.up
	d.extensions.mu.Unlock()
	if !up {
		return errExtensionHostDown
	}
	err := d.loadExtensions(ctx)
	if errors.Is(err, errExtensionLoadFailed) {
		d.extensions.host.Restart()
		return fmt.Errorf("%w; the extension host restarts and loads again", err)
	}
	return err
}

var (
	errExtensionsOff       = errors.New("extensions are not available in this daemon")
	errExtensionHostDown   = errors.New("the extension host is starting or restarting; try again in a moment")
	errExtensionLoadFailed = errors.New("the extension host did not finish loading")
)

func (r *extensionRuntime) extensionByToken(token string) (string, []string, bool) {
	if r == nil || token == "" {
		return "", nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.tokens[token]
	if !ok {
		return "", nil, false
	}
	loaded := r.loaded[id]
	return id, slices.Clone(loaded.held), true
}

func (r *extensionRuntime) find(id string) (*loadedExtension, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	loaded, ok := r.loaded[id]
	return loaded, ok
}

func (d *Daemon) runExtensionJobs(ctx context.Context) {
	ticker := time.NewTicker(extensionJobTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.startDueExtensionJobs(ctx, now)
		}
	}
}

func (d *Daemon) startDueExtensionJobs(ctx context.Context, now time.Time) {
	r := d.extensions
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.up {
		return
	}
	for id, loaded := range r.loaded {
		if loaded.host.State != "loaded" {
			continue
		}
		for _, job := range loaded.host.Jobs {
			state := loaded.jobs[job.Name]
			if state == nil || state.running || now.Before(state.due) {
				continue
			}
			state.running = true
			go func() { _ = d.runExtensionJob(ctx, id, job, state) }()
		}
	}
}

func (d *Daemon) runExtensionJob(ctx context.Context, id string, job extensionJob, state *extensionJobState) error {
	r := d.extensions
	runCtx, cancel := context.WithTimeout(ctx, extensionJobLimit)
	err := r.host.Client().Call(runCtx, "job", map[string]string{"id": id, "name": job.Name}, nil)
	cancel()
	finished := time.Now()
	r.mu.Lock()
	state.running = false
	state.LastRun = finished
	state.due = finished.Add(state.every)
	state.LastError = ""
	if err != nil {
		state.LastError = err.Error()
	}
	r.mu.Unlock()
	if err != nil && ctx.Err() == nil && !job.Source {
		d.log.Printf("extension %s: job %s: %s", id, job.Name, err)
	}
	if job.Source && ctx.Err() == nil {
		d.publishEvent("extension.source", "", map[string]any{"extension": id, "source": job.Name, "ok": err == nil})
	}
	return err
}

// pumpExtensionEvents hands every daemon event to the extensions that hold
// read.all. Its cursor outlives host restarts, so events published while the
// host was down are delivered once it is back.
func (d *Daemon) pumpExtensionEvents(ctx context.Context) {
	_, follower, _ := d.events.subscribe(d.events.cursor())
	defer follower.Cancel()
	var queued []apiEvent
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, open := <-follower.Wake:
			if !open {
				return
			}
			events, _ := follower.read()
			queued = append(queued, events...)
			if len(queued) > eventHistoryLimit {
				queued = queued[len(queued)-eventHistoryLimit:]
			}
		case <-retry.C:
		}
		for len(queued) > 0 {
			batch := queued[:min(len(queued), extensionEventBatch)]
			if err := d.deliverExtensionEvents(ctx, batch); err != nil {
				retry.Reset(time.Second)
				break
			}
			queued = queued[len(batch):]
		}
	}
}

func (d *Daemon) deliverExtensionEvents(ctx context.Context, events []apiEvent) error {
	r := d.extensions
	r.mu.Lock()
	up := r.up && !r.reloading
	var to []string
	for id, loaded := range r.loaded {
		if loaded.host.State == "loaded" && loaded.host.Events && slices.Contains(loaded.held, "read.all") {
			to = append(to, id)
		}
	}
	r.mu.Unlock()
	if !up {
		return errExtensionHostDown
	}
	if len(to) == 0 {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return r.host.Client().Call(callCtx, "events", map[string]any{"events": events, "to": to}, nil)
}

type extensionView struct {
	ID           string                        `json:"id"`
	Label        string                        `json:"label"`
	Description  string                        `json:"description,omitempty"`
	State        string                        `json:"state"`
	Error        string                        `json:"error,omitempty"`
	Page         string                        `json:"page,omitempty"`
	Capabilities []string                      `json:"capabilities"`
	Pending      []string                      `json:"pendingCapabilities,omitempty"`
	Actions      []string                      `json:"actions"`
	Jobs         map[string]*extensionJobState `json:"jobs,omitempty"`
	EventError   string                        `json:"lastEventError,omitempty"`
	Mounts       []mountState                  `json:"mounts,omitempty"`
}

type extensionsView struct {
	Host       string          `json:"host"`
	HostError  string          `json:"hostError,omitempty"`
	Extensions []extensionView `json:"extensions"`
}

func (d *Daemon) extensionsView() extensionsView {
	r := d.extensions
	if r == nil {
		return extensionsView{Host: "off", Extensions: []extensionView{}}
	}
	mounts, labels := r.mounts.view()
	r.mu.Lock()
	defer r.mu.Unlock()
	view := extensionsView{Host: "running", Extensions: []extensionView{}}
	if !r.up {
		view.Host, view.HostError = "unavailable", r.hostErr
	}
	for _, loaded := range r.loaded {
		item := extensionView{
			ID:           loaded.ext.ID,
			Label:        loaded.ext.Manifest.Label,
			Description:  loaded.ext.Manifest.Description,
			State:        loaded.host.State,
			Error:        loaded.host.Error,
			Capabilities: append([]string{}, loaded.held...),
			Pending:      loaded.pending,
			Actions:      append([]string{}, loaded.host.Actions...),
			EventError:   loaded.host.LastEventError,
		}
		if item.Label == "" {
			item.Label = loaded.ext.ID
		}
		if loaded.ext.HasPage {
			item.Page = "/x/" + loaded.ext.ID + "/"
		}
		item.Mounts = mounts[loaded.ext.ID]
		delete(mounts, loaded.ext.ID)
		if len(loaded.jobs) > 0 {
			item.Jobs = map[string]*extensionJobState{}
			for name, state := range loaded.jobs {
				copied := *state
				item.Jobs[name] = &copied
			}
		}
		view.Extensions = append(view.Extensions, item)
	}
	for id, list := range mounts {
		view.Extensions = append(view.Extensions, extensionView{ID: id, Label: labels[id], State: "not loaded", Capabilities: []string{}, Actions: []string{}, Mounts: list})
	}
	slices.SortFunc(view.Extensions, func(a, b extensionView) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return view
}
