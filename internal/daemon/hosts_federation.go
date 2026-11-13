package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/buildinfo"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sse"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	hostStateLocal        = "local"
	hostStateConnecting   = "connecting"
	hostStateConnected    = "connected"
	hostStateUnreachable  = "unreachable"
	hostStateUnauthorized = "unauthorized"
	hostStateIncompatible = "incompatible"

	hostRetryMin = time.Second
	hostRetryMax = 30 * time.Second
)

type dashboardHost struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

type federation struct {
	// lifecycle serializes pairing changes: attaching or detaching a host
	// together with the registry write that pairs or removes it. Host loops
	// never take it, so holding it while joining them cannot deadlock.
	lifecycle sync.Mutex
	mu        sync.Mutex
	hosts     map[string]*pairedHost
	rootCAs   *x509.CertPool
	ctx       context.Context
	wg        sync.WaitGroup
	running   bool
	downGrace time.Duration
	// ledgerSyncEvery bounds how long a change on a project's owner waits to
	// reach this host, which pulls it.
	ledgerSyncEvery time.Duration
	retryMin        time.Duration
	// Set before the per-host loops start; they read it unlocked.
	patience time.Duration
}

type pairedHost struct {
	record registry.PairedHost
	client *hosts.Client
	cancel context.CancelFunc
	done   sync.WaitGroup
	mu     sync.Mutex
	state  string
	detail string
	epoch  string
	build  buildinfo.Identity
	sims   bool
	simsAt time.Time
	drain  sync.Mutex
	wake   chan struct{}
	// tokenProblem, guarded by mu, says how to pair again when this daemon's
	// token on the host does not speak for this host.
	tokenProblem string

	ledgerMu        sync.Mutex
	ledgerWake      chan struct{}
	ledgerConfirmed map[string]bool
	// hidden holds rows the host serves whose id is already a local session,
	// guarded by mu. Only the mirror loop writes it.
	hidden  map[string]dashboardSession
	refused map[string]dashboardSession
}

func newFederation() *federation {
	return &federation{hosts: map[string]*pairedHost{}, downGrace: hostDownGrace, ledgerSyncEvery: ledgerSyncInterval, retryMin: hostRetryMin, patience: hostDeliveryPatience}
}

func (h *pairedHost) status() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, h.detail
}

func (h *pairedHost) pairingProblem() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tokenProblem
}

func (d *Daemon) checkHostToken(ctx context.Context, host *pairedHost) {
	device, known, err := host.client.TokenDevice(ctx)
	if err != nil {
		return
	}
	problem := ""
	if known && (!registry.Device{Scopes: device.Scopes}.IsHost() || device.HostID != d.hostIdentity().ID) {
		problem = hosts.Unproven(host.record.Name)
	}
	host.mu.Lock()
	changed := host.tokenProblem != problem
	host.tokenProblem = problem
	host.mu.Unlock()
	if changed && problem != "" {
		d.log.Printf("host %s: %s", host.record.Name, problem)
	}
	if changed {
		d.invalidateDashboard()
	}
}

func (h *pairedHost) setSimulators(runs bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sims, h.simsAt = runs, time.Now()
}

func (h *pairedHost) simulatorsStale(age time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.simsAt) >= age
}

func (h *pairedHost) runsSimulators() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sims
}

func (h *pairedHost) setBuild(build buildinfo.Identity) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.build = build
}

// reportedBuild is the build the host last named, or nil before it answered
// or when it does not name one.
func (h *pairedHost) reportedBuild() *buildinfo.Identity {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.build.Version == "" {
		return nil
	}
	build := h.build
	return &build
}

func (h *pairedHost) setHidden(id string, row *dashboardSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if row == nil {
		delete(h.hidden, id)
		return
	}
	if h.hidden == nil {
		h.hidden = map[string]dashboardSession{}
	}
	h.hidden[id] = *row
}

func (h *pairedHost) hiddenRow(id string) (dashboardSession, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	row, found := h.hidden[id]
	return row, found
}

func (h *pairedHost) hiddenRows() []dashboardSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows := make([]dashboardSession, 0, len(h.hidden))
	for _, row := range h.hidden {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

// notice names the host's sessions this daemon does not show because their
// conversation is also a local session.
func (h *pairedHost) notice() string {
	rows := h.hiddenRows()
	switch len(rows) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("1 session on %s not shown: conversation %s is also a local session", h.record.Name, rows[0].ID)
	}
	ids := make([]string, len(rows))
	for index, row := range rows {
		ids[index] = row.ID
	}
	return fmt.Sprintf("%d sessions on %s not shown: conversations %s are also local sessions", len(rows), h.record.Name, strings.Join(ids, ", "))
}

func (h *pairedHost) ref() dashboardHost {
	state, _ := h.status()
	return dashboardHost{ID: h.record.ID, Name: h.record.Name, State: state}
}

func (d *Daemon) hostIdentity() registry.HostIdentity {
	d.identityMu.RLock()
	defer d.identityMu.RUnlock()
	return d.identity
}

func (d *Daemon) setHostIdentity(identity registry.HostIdentity) {
	d.identityMu.Lock()
	defer d.identityMu.Unlock()
	d.identity = identity
}

func (d *Daemon) isLocalHostRef(ref string) bool {
	identity := d.hostIdentity()
	return strings.EqualFold(ref, identity.ID) || strings.EqualFold(ref, identity.Name)
}

func (d *Daemon) localHost() dashboardHost {
	identity := d.hostIdentity()
	return dashboardHost{ID: identity.ID, Name: identity.Name, State: hostStateLocal}
}

func (d *Daemon) startFederation(ctx context.Context) {
	d.federation.mu.Lock()
	d.federation.ctx = ctx
	d.federation.running = true
	d.federation.wg.Add(2)
	d.federation.mu.Unlock()
	go func() {
		defer d.federation.wg.Done()
		d.followHostDeliveries(ctx)
	}()
	go func() {
		defer d.federation.wg.Done()
		d.followMoveIns(ctx)
	}()
	d.failInterruptedMoves()
	paired, err := d.Registry.ListHosts()
	if err != nil {
		d.log.Printf("paired hosts: %s", err)
		return
	}
	for _, record := range paired {
		if err := d.attachHost(record); err != nil {
			d.log.Printf("host %s: %s", record.Name, err)
		}
	}
}

func (d *Daemon) stopFederation() {
	d.federation.mu.Lock()
	d.federation.running = false
	for _, host := range d.federation.hosts {
		host.cancel()
	}
	d.federation.mu.Unlock()
	d.federation.wg.Wait()
}

func (d *Daemon) attachHost(record registry.PairedHost) error {
	d.federation.lifecycle.Lock()
	defer d.federation.lifecycle.Unlock()
	return d.attachHostLocked(record)
}

// attachHostLocked replaces any running loops for the host with a fresh pair.
// The caller holds federation.lifecycle; a host no longer in the registry is
// never attached.
func (d *Daemon) attachHostLocked(record registry.PairedHost) error {
	client, err := hosts.NewClient(record.Endpoint, record.Address, record.Token, hosts.ClientOptions{RootCAs: d.federation.rootCAs, HostID: record.ID})
	if err != nil {
		return err
	}
	if current, err := d.Registry.Host(record.ID); err != nil || current.ID != record.ID {
		return fmt.Errorf("host %s is no longer paired", record.Name)
	}
	d.detachHostLocked(record.ID)
	d.federation.mu.Lock()
	defer d.federation.mu.Unlock()
	parent := d.federation.ctx
	if parent == nil || !d.federation.running {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	host := &pairedHost{record: record, client: client, cancel: cancel, state: hostStateConnecting, wake: make(chan struct{}, 1), ledgerWake: make(chan struct{}, 1)}
	d.federation.hosts[record.ID] = host
	if d.federation.running {
		d.federation.wg.Add(3)
		host.done.Add(3)
		go func() {
			defer d.federation.wg.Done()
			defer host.done.Done()
			d.runHostLedgerSync(ctx, host)
		}()
		go func() {
			defer d.federation.wg.Done()
			defer host.done.Done()
			d.runHostMirror(ctx, host)
		}()
		go func() {
			defer d.federation.wg.Done()
			defer host.done.Done()
			d.runHostDeliveries(ctx, host)
		}()
	}
	return nil
}

// detachHostLocked returns once the host's mirror and delivery loops have
// exited, so no write from them can land after the caller removes the host's
// rows. The caller holds federation.lifecycle.
func (d *Daemon) detachHostLocked(hostID string) {
	d.federation.mu.Lock()
	host, found := d.federation.hosts[hostID]
	delete(d.federation.hosts, hostID)
	d.federation.mu.Unlock()
	if found {
		host.cancel()
		host.done.Wait()
	}
}

// hostConnected reports whether a paired host is reachable and compatible
// now, the only time its sessions are offered as peers here.
func (d *Daemon) hostConnected(hostID string) bool {
	host, found := d.pairedHost(hostID)
	if !found {
		return false
	}
	state, _ := host.status()
	return state == hostStateConnected
}

func (d *Daemon) pairedHost(ref string) (*pairedHost, bool) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	d.federation.mu.Lock()
	defer d.federation.mu.Unlock()
	if host, found := d.federation.hosts[ref]; found {
		return host, true
	}
	for _, host := range d.federation.hosts {
		if host.record.Name == ref {
			return host, true
		}
	}
	return nil, false
}

func (d *Daemon) pairedHosts() []*pairedHost {
	d.federation.mu.Lock()
	defer d.federation.mu.Unlock()
	result := make([]*pairedHost, 0, len(d.federation.hosts))
	for _, host := range d.federation.hosts {
		result = append(result, host)
	}
	return result
}

var errUnknownHost = errors.New("unknown host")

// routeSession is the single owner of host routing. A nil host means the
// session belongs to this daemon and rawID is its stored id.
func (d *Daemon) routeSession(ref string) (*pairedHost, string, error) {
	ref = strings.TrimSpace(ref)
	if hostRef, target, qualified := hosts.Split(ref); qualified {
		if d.isLocalHostRef(hostRef) {
			id, owner, found, err := d.Registry.CanonicalSession(target)
			if err != nil {
				return nil, "", err
			}
			if !found {
				return nil, target, nil
			}
			if owner != "" {
				return nil, "", registry.SessionOwnedError{SessionID: id, HostID: owner}
			}
			return nil, id, nil
		}
		host, found := d.pairedHost(hostRef)
		if !found {
			return nil, "", fmt.Errorf("%w %q", errUnknownHost, hostRef)
		}
		return host, target, nil
	}
	if ref == "" {
		return nil, ref, nil
	}
	id, owner, found, err := d.Registry.CanonicalSession(ref)
	if err != nil {
		return nil, "", err
	}
	if !found || owner == "" {
		if found {
			ref = id
		}
		return nil, ref, nil
	}
	host, paired := d.pairedHost(owner)
	if !paired {
		return nil, "", fmt.Errorf("%w %q", errUnknownHost, owner)
	}
	return host, id, nil
}

func (d *Daemon) setHostState(host *pairedHost, state, detail string, contacted bool) {
	host.mu.Lock()
	changed := host.state != state || host.detail != detail
	host.state, host.detail = state, detail
	host.mu.Unlock()
	if err := d.Registry.RecordHostContact(host.record.ID, state, detail, contacted); err != nil {
		d.log.Printf("host %s state: %s", host.record.Name, err)
	}
	if changed {
		d.publishEvent("host.state", host.record.ID, map[string]any{"host": host.ref(), "detail": detail})
	}
	if changed && state == hostStateConnected {
		d.wakeLedgerSync(host)
	}
}

func (d *Daemon) runHostMirror(ctx context.Context, host *pairedHost) {
	delay := d.federation.retryMin
	var outage hostOutage
	var repair repairBackoff
	for ctx.Err() == nil {
		connected, err := d.mirrorHostOnce(ctx, host, &outage)
		if ctx.Err() != nil {
			return
		}
		if connected {
			delay = d.federation.retryMin
		}
		wait := delay
		var unapplied catalogApplyError
		if errors.As(err, &unapplied) {
			wait = repair.next(time.Now(), d.federation.retryMin, hostRetryMax)
		}
		if err != nil {
			d.hostFailed(host, &outage, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		delay = min(delay*2, hostRetryMax)
	}
}

// repairBackoff spaces the snapshots that repair catalog events the follower
// could not apply. The first is taken at once. A failure within ceiling of
// the follower resuming waits a doubling delay instead, since a payload shape
// this build cannot read fails on every event that carries it.
type repairBackoff struct {
	resumed time.Time
	delay   time.Duration
}

func (b *repairBackoff) next(now time.Time, floor, ceiling time.Duration) time.Duration {
	switch {
	case b.resumed.IsZero() || now.Sub(b.resumed) > ceiling:
		b.delay = 0
	case b.delay == 0:
		b.delay = floor
	default:
		b.delay = min(b.delay*2, ceiling)
	}
	b.resumed = now.Add(b.delay)
	return b.delay
}

func hostFailureState(err error) string {
	var status hosts.StatusError
	var mismatch hosts.ErrProtocolMismatch
	switch {
	case errors.As(err, &status) && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden):
		return hostStateUnauthorized
	case errors.As(err, &mismatch), errors.Is(err, hosts.ErrHostIdentityChanged):
		return hostStateIncompatible
	default:
		return hostStateUnreachable
	}
}

func (d *Daemon) markHostDown(host *pairedHost, cause error) {
	state := hostFailureState(cause)
	detail := cause.Error()
	d.setHostState(host, state, detail, false)
	reason := hostUnavailableReason(host.record.Name, state, detail)
	rows, err := d.Registry.HostUnretiredCatalogRows(host.record.ID)
	if err != nil {
		d.log.Printf("host %s rows: %s", host.record.Name, err)
		return
	}
	ref := host.ref()
	for _, row := range rows {
		var projection dashboardSession
		if json.Unmarshal([]byte(row.ProjectionFingerprint), &projection) != nil {
			continue
		}
		projection.Host = &ref
		projection.UnavailableReason = reason
		projection.QueueCount = row.HostQueueCount
		if err := d.applyMirroredProjection(host.record.ID, row.Session, projection, row.Project, row.Checkout); err != nil {
			d.log.Printf("host %s mark unavailable: %s", host.record.Name, err)
		}
	}
}

func hostUnavailableReason(name, state, detail string) string {
	switch state {
	case hostStateUnauthorized:
		return fmt.Sprintf("host %s rejected this daemon's token; pair it again", name)
	case hostStateIncompatible:
		return fmt.Sprintf("host %s is incompatible: %s", name, detail)
	default:
		return fmt.Sprintf("host %s is unreachable: %s", name, detail)
	}
}

type remoteSnapshot struct {
	HostEpoch    string                      `json:"hostEpoch"`
	CatalogSeq   uint64                      `json:"catalogSeq"`
	Sessions     []dashboardSession          `json:"sessions"`
	RetiredClock *int64                      `json:"retiredClock"`
	RetiredIDs   []string                    `json:"retiredIds"`
	Coordinators []registry.CoordinatorEntry `json:"coordinators"`
	OwnDecisions string                      `json:"ownDecisionsRev"`
	ProjectLinks []registry.ProjectLink      `json:"projectLinks"`
	ProjectMoves []registry.ProjectMove      `json:"projectMoves"`
	MovedOut     []registry.MovedSession     `json:"movedSessions"`
}

func (d *Daemon) mirrorHostOnce(ctx context.Context, host *pairedHost, outage *hostOutage) (bool, error) {
	version, err := host.client.Version(ctx)
	if err != nil {
		return false, err
	}
	host.setBuild(version.Identity)
	if version.Simulators != nil {
		host.setSimulators(*version.Simulators)
	}
	// During an outage only a snapshot proves the host serves again; a version that answers alone does not end it.
	answered := outage.since.IsZero()
	if answered {
		d.hostAnswered(host, outage)
	}
	d.checkHostToken(ctx, host)
	for ctx.Err() == nil {
		seq, err := d.reconcileHost(ctx, host)
		if err != nil {
			return true, err
		}
		if !answered {
			answered = true
			d.hostAnswered(host, outage)
			continue
		}
		d.drainHostDeliveries(ctx, host)
		d.resumeHostMoves(ctx, host)
		gap, err := d.followHostCatalog(ctx, host, seq)
		if !gap {
			return true, err
		}
	}
	return true, ctx.Err()
}

func (d *Daemon) reconcileHost(ctx context.Context, host *pairedHost) (uint64, error) {
	var snapshot remoteSnapshot
	query, err := d.mirrorSnapshotQuery(host.record.ID)
	if err != nil {
		return 0, localMirror(err)
	}
	if err := host.client.GetJSON(ctx, "/api/v1/snapshot", query, &snapshot); err != nil {
		return 0, err
	}
	host.mu.Lock()
	host.hidden, host.refused = nil, nil
	host.mu.Unlock()
	keep := map[string]struct{}{}
	complete := snapshot.RetiredClock != nil
	for _, row := range snapshot.Sessions {
		if !servedByHost(row, host.record.ID) {
			continue
		}
		if err := d.mirrorOrHide(host, row, false); err != nil {
			d.logRuntimeError("host "+host.record.Name+" mirror "+row.ID, err)
			complete = false
			continue
		}
		keep[row.ID] = struct{}{}
	}
	existing, err := d.Registry.HostCatalogRowIDs(host.record.ID)
	if err != nil {
		return 0, localMirror(err)
	}
	retired := heldRetired(snapshot)
	present := make(map[string]struct{}, len(existing))
	held := []string{}
	for _, id := range existing {
		present[id] = struct{}{}
		if _, found := keep[id]; found {
			continue
		}
		if _, found := retired[id]; found {
			held = append(held, id)
			continue
		}
		if err := d.removeHostRow(host.record.ID, id, "omitted_by_host"); err != nil {
			return 0, localMirror(err)
		}
	}
	if err := d.Registry.RetireHostRows(host.record.ID, held); err != nil {
		return 0, localMirror(err)
	}
	for id := range retired {
		if _, found := present[id]; !found {
			if _, hidden := host.hiddenRow(id); !hidden {
				complete = false
			}
		}
	}
	retiredClock := int64(-1)
	if complete {
		retiredClock = *snapshot.RetiredClock
	}
	if err := d.Registry.RecordHostRetiredClock(host.record.ID, retiredClock); err != nil {
		return 0, localMirror(err)
	}
	coordinators := make([]registry.CoordinatorEntry, 0, len(snapshot.Coordinators))
	for _, entry := range snapshot.Coordinators {
		if entry, ok := mirroredCoordinator(host.record.ID, entry); ok {
			coordinators = append(coordinators, entry)
		}
	}
	if err := d.Registry.ReplaceHostCoordinators(host.record.ID, coordinators); err != nil {
		return 0, localMirror(err)
	}
	if _, err := d.adoptProjectMoves(snapshot.ProjectMoves); err != nil {
		return 0, localMirror(err)
	}
	if err := d.adoptProjectLinks(snapshot.ProjectLinks); err != nil {
		return 0, localMirror(err)
	}
	for _, moved := range snapshot.MovedOut {
		d.handOverHostSession(host, moved.SessionID, moved.MovedToHost)
	}
	host.epoch = snapshot.HostEpoch
	if err := d.Registry.RecordHostCatalogCursor(host.record.ID, snapshot.HostEpoch, snapshot.CatalogSeq); err != nil {
		return 0, localMirror(err)
	}
	d.decisions.setPeer(host.record.ID, snapshot.OwnDecisions)
	return snapshot.CatalogSeq, nil
}

// servedByHost keeps a mirror to sessions the remote serves itself. Rows the
// remote mirrors from its own paired hosts are never re-exported, so hosts
// paired in both directions do not loop.
func servedByHost(row dashboardSession, hostID string) bool {
	if row.ID == "" || strings.Contains(row.ID, hosts.Separator) {
		return false
	}
	return row.Host == nil || row.Host.ID == hostID
}

func (d *Daemon) followHostCatalog(ctx context.Context, host *pairedHost, after uint64) (bool, error) {
	response, err := host.client.Open(ctx, "/api/v1/catalog-events", url.Values{"after": []string{strconv.FormatUint(after, 10)}}, http.Header{"Accept": []string{"text/event-stream"}})
	if err != nil {
		return false, err
	}
	if response.StatusCode == http.StatusConflict {
		response.Body.Close()
		return true, nil
	}
	if response.StatusCode != http.StatusOK {
		_, err := hosts.ReadResponse(response)
		return false, err
	}
	defer response.Body.Close()
	reader := sse.NewReader(response.Body)
	refused := map[string]bool{}
	for {
		event, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return false, fmt.Errorf("host %s catalog stream: %w", host.record.Name, err)
		}
		if !event.HasData {
			continue
		}
		var envelope catalogStreamEnvelope
		if err := json.Unmarshal([]byte(event.Data), &envelope); err != nil {
			return false, fmt.Errorf("host %s sent an invalid catalog event: %w", host.record.Name, err)
		}
		if err := d.applyHostCatalogEvent(host, envelope); err != nil {
			var owned registry.SessionOwnedError
			if !errors.As(err, &owned) {
				return false, catalogApplyError{host: host.record.Name, seq: envelope.Seq, err: err}
			}
			if !refused[owned.SessionID] {
				refused[owned.SessionID] = true
				d.logRuntimeError("host "+host.record.Name+" catalog event", err)
			}
		}
		if err := d.Registry.RecordHostCatalogCursor(host.record.ID, host.epoch, envelope.Seq); err != nil {
			return false, localMirror(err)
		}
	}
	return false, fmt.Errorf("host %s closed the catalog stream", host.record.Name)
}

func (d *Daemon) applyHostCatalogEvent(host *pairedHost, envelope catalogStreamEnvelope) error {
	switch envelope.Type {
	case "session.upserted":
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		if !servedByHost(payload.Session, host.record.ID) {
			return nil
		}
		return d.mirrorOrHide(host, payload.Session, true)
	case "session.removed":
		var payload struct {
			SessionID   string `json:"sessionId"`
			MovedToHost string `json:"movedToHost"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		if payload.SessionID == "" || strings.Contains(payload.SessionID, hosts.Separator) {
			return nil
		}
		host.dropRefused(payload.SessionID)
		if payload.MovedToHost != "" && d.handOverHostSession(host, payload.SessionID, payload.MovedToHost) {
			return nil
		}
		host.setHidden(payload.SessionID, nil)
		return d.removeHostRow(host.record.ID, payload.SessionID, "removed_by_host")
	case "session.queue":
		var payload struct {
			SessionID  string `json:"sessionId"`
			QueueCount int    `json:"queueCount"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		return d.updateMirroredQueueCount(host, payload.SessionID, payload.QueueCount)
	case "coordinator.changed":
		var entry registry.CoordinatorEntry
		if err := json.Unmarshal(envelope.Data, &entry); err != nil {
			return err
		}
		if entry, ok := mirroredCoordinator(host.record.ID, entry); ok {
			return d.Registry.PutHostCoordinator(host.record.ID, entry)
		}
	case "coordinator.removed":
		var payload struct {
			ProjectID string `json:"projectId"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		if entry, ok := mirroredCoordinator(host.record.ID, registry.CoordinatorEntry{ProjectID: payload.ProjectID, Name: "-"}); ok {
			return d.Registry.RemoveHostCoordinator(host.record.ID, entry.ProjectID)
		}
	case "project.linked":
		var link registry.ProjectLink
		if err := json.Unmarshal(envelope.Data, &link); err != nil {
			return err
		}
		return d.adoptProjectLinks([]registry.ProjectLink{link})
	case "project.moved":
		var move registry.ProjectMove
		if err := json.Unmarshal(envelope.Data, &move); err != nil {
			return err
		}
		_, err := d.adoptProjectMoves([]registry.ProjectMove{move})
		return err
	case "decisions.changed":
		var payload struct {
			Own string `json:"own"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		d.decisions.setPeer(host.record.ID, payload.Own)
	}
	return nil
}

// mirroredCoordinator names a paired host's coordinator as this host does:
// a project id without a repository key is that host's own and is qualified
// with it. A host publishes only its own coordinators, so an id already
// qualified is refused rather than mirrored back.
func mirroredCoordinator(hostID string, entry registry.CoordinatorEntry) (registry.CoordinatorEntry, bool) {
	if entry.ProjectID == "" || entry.Name == "" {
		return entry, false
	}
	if _, _, qualified := hosts.Split(entry.ProjectID); qualified {
		return entry, false
	}
	entry.ProjectID = hosts.Qualify(hostID, entry.ProjectID)
	entry.SessionID = hosts.Qualify(hostID, entry.SessionID)
	return entry, true
}

func (d *Daemon) updateMirroredQueueCount(host *pairedHost, sessionID string, count int) error {
	rows, err := d.Registry.HostUnretiredCatalogRows(host.record.ID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Session.ID != sessionID {
			continue
		}
		var projection dashboardSession
		if err := json.Unmarshal([]byte(row.ProjectionFingerprint), &projection); err != nil {
			return err
		}
		projection.QueueCount = count
		return d.applyMirroredProjection(host.record.ID, row.Session, projection, row.Project, row.Checkout)
	}
	return nil
}

// mirrorOrHide mirrors a host row, or records it as hidden when its id is
// already a local session. A local session row is never deleted, so a hidden
// row stays hidden until the host stops serving it. A live row, one the
// host's catalog stream carried rather than a snapshot, notifies a turn the
// host has just notified.
func (d *Daemon) mirrorOrHide(host *pairedHost, row dashboardSession, live bool) error {
	err := d.mirrorRemoteRow(host, row, live)
	var owned registry.SessionOwnedError
	if errors.As(err, &owned) && owned.HostID == "" {
		host.setHidden(row.ID, &row)
		return nil
	}
	if errors.As(err, &owned) && owned.HostID != host.record.ID {
		host.keepRefused(row)
	}
	if err == nil {
		host.setHidden(row.ID, nil)
		host.dropRefused(row.ID)
	}
	return err
}

func (d *Daemon) mirrorRemoteRow(host *pairedHost, row dashboardSession, live bool) error {
	rawID := row.ID
	session := surface.Session{ID: row.ID, Surface: row.Surface, RuntimeKind: row.RuntimeKind, Settings: row.Settings, Name: row.Name, Cwd: row.Cwd, Status: row.Status, Ended: row.Ended, LastActive: row.LastActive, Source: row.Source, Transport: row.Transport}
	if row.Subagent != nil {
		subagent := *row.Subagent
		session.Subagent = &subagent
		qualified := subagent
		qualified.ParentID = hosts.Qualify(host.record.ID, subagent.ParentID)
		qualified.RootID = hosts.Qualify(host.record.ID, subagent.RootID)
		row.Subagent = &qualified
	}
	row.Delegated = session.Delegated()
	if row.Project != nil {
		qualified := *row.Project
		qualified.ID = hosts.Qualify(host.record.ID, qualified.ID)
		qualified.LocalID = hosts.Qualify(host.record.ID, qualified.LocalID)
		row.Project = &qualified
	}
	if row.Checkout != nil {
		qualified := *row.Checkout
		qualified.ID = hosts.Qualify(host.record.ID, qualified.ID)
		row.Checkout = &qualified
	}
	project, _ := json.Marshal(row.Project)
	checkout, _ := json.Marshal(row.Checkout)
	row.ID = hosts.Qualify(host.record.ID, row.ID)
	if row.ForkedFrom != "" {
		row.ForkedFrom = hosts.Qualify(host.record.ID, row.ForkedFrom)
	}
	row.StartedBy = hosts.Qualify(host.record.ID, row.StartedBy)
	if ref, id, qualified := hosts.Split(row.StartedBy); qualified && d.isLocalHostRef(ref) {
		row.StartedBy = id
	}
	if row.Runtime != nil {
		runtime := *row.Runtime
		runtime.Focusable = false
		runtime.Location = nil
		row.Runtime = &runtime
	}
	ref := host.ref()
	row.Host = &ref
	var prior mirroredRow
	priorFound := false
	if live {
		stored, found, err := d.Registry.CatalogProjection(rawID)
		if err != nil {
			return err
		}
		prior, priorFound = mirroredState(stored), found
	}
	if err := d.applyMirroredProjection(host.record.ID, session, row, project, checkout); err != nil {
		return err
	}
	if priorFound {
		d.notifyMirroredCompletion(host, rawID, row, prior.TurnCompletion)
		d.notifyMirroredAsk(host, rawID, row, prior.Ask)
	}
	return nil
}

func (d *Daemon) applyMirroredProjection(hostID string, session surface.Session, projection dashboardSession, project, checkout json.RawMessage) error {
	registered := projection.RegisteredAt
	projection.ObservedAt, projection.RegisteredAt = time.Time{}, time.Time{}
	projection.Freshness = nil
	projection.Placement = ""
	encoded, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	return d.publishHostRow(hostID, registry.HostCatalogRow{Session: session, EntityID: projection.ID, Projection: encoded, Project: project, Checkout: checkout, UnavailableReason: projection.UnavailableReason, ObservedAt: time.Now().UTC(), RegisteredAt: registered, QueueCount: projection.QueueCount})
}

func (d *Daemon) refreshHostQueueCounts() {
	d.catalog.mu.Lock()
	defer d.catalog.mu.Unlock()
	changed, err := d.Registry.RefreshHostQueueCounts()
	if err != nil {
		d.logRuntimeError("host queue counts", err)
		return
	}
	if changed > 0 {
		d.publishCommittedHostWriteLocked("host queue counts")
	}
}

// publishHostRow and removeHostRow return only whether the registry write
// committed. A later publication failure is logged and left to catalog
// subscribers, who reconcile from a snapshot after a gap; callers must never
// treat a committed mirror row as absent.
func (d *Daemon) publishHostRow(hostID string, row registry.HostCatalogRow) error {
	d.catalog.mu.Lock()
	defer d.catalog.mu.Unlock()
	if _, _, err := d.Registry.ApplyHostCatalogRow(hostID, row); err != nil {
		return err
	}
	d.publishCommittedHostWriteLocked("host " + hostID + " mirror " + row.Session.ID)
	return nil
}

func (d *Daemon) removeHostRow(hostID, sessionID, reason string) error {
	d.catalog.mu.Lock()
	defer d.catalog.mu.Unlock()
	if _, _, err := d.Registry.RemoveHostCatalogRow(hostID, sessionID, hosts.Qualify(hostID, sessionID), reason); err != nil {
		return err
	}
	d.publishCommittedHostWriteLocked("host " + hostID + " remove " + sessionID)
	return nil
}

func (d *Daemon) publishCommittedHostWriteLocked(what string) {
	if err := d.catalog.flushCommittedLocked(); err != nil {
		d.logRuntimeError(what+" catalog", err)
	}
}

const (
	hostDeliveryInterval = 5 * time.Second
	hostDeliveryPatience = 15 * time.Minute
)

func (d *Daemon) runHostDeliveries(ctx context.Context, host *pairedHost) {
	ticker := time.NewTicker(hostDeliveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-host.wake:
		}
		if state, _ := host.status(); state == hostStateConnected {
			d.drainHostDeliveries(ctx, host)
		}
	}
}

// followHostDeliveries wakes every host's delivery loop when any process holds
// a send for a host, so a held send to a connected host goes out at once.
func (d *Daemon) followHostDeliveries(ctx context.Context) {
	path, err := d.Registry.ChangeSignal(registry.ChangeHostDeliveries)
	if err != nil {
		d.log.Printf("follow held host sends, sending them on the delivery tick: %s", err)
		return
	}
	wake := filewake.Follow(path)
	defer wake.Release()
	if wake.C() == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake.C():
			d.federation.mu.Lock()
			for _, host := range d.federation.hosts {
				select {
				case host.wake <- struct{}{}:
				default:
				}
			}
			d.federation.mu.Unlock()
		}
	}
}

// drainHostDeliveries replays held sends with their original key and body.
// The host's action receipts make a replay of an applied request return the
// stored outcome, so this never produces a second turn.
func (d *Daemon) drainHostDeliveries(ctx context.Context, host *pairedHost) {
	host.drain.Lock()
	defer host.drain.Unlock()
	defer d.drainHostHeldActions(ctx, host)
	pending, err := d.Registry.HostDeliveries(host.record.ID)
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" deliveries", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	defer d.refreshHostQueueCounts()
	for _, item := range pending {
		if state, _ := host.status(); ctx.Err() != nil || state != hostStateConnected {
			return
		}
		d.replayHostDelivery(ctx, host, item)
	}
}

func (d *Daemon) replayHostDelivery(ctx context.Context, host *pairedHost, item registry.HostDelivery) {
	inflightAt, claimed, err := d.Registry.ClaimHostDelivery(item.ID)
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" delivery", err)
		return
	}
	if !claimed {
		return
	}
	answer := d.postHostDelivery(ctx, host, item)
	var moved hosts.StatusError
	if errors.As(answer.readErr, &moved) && moved.Code == movedCode {
		if owner := host; d.ownedBy(owner, item.SessionID) {
			defer d.handOverHostSession(owner, item.SessionID, moved.MovedToHost)
		}
		if next, found := d.pairedHost(moved.MovedToHost); found && next != host {
			host, answer = next, d.postHostDelivery(ctx, next, item)
		}
	}
	unconfirmed := func() {
		if time.Since(inflightAt) > d.federation.patience {
			_ = d.Registry.DeadLetterUnknown(item.ID, fmt.Errorf("host %s has not confirmed the message", host.record.Name))
		}
	}
	if err := answer.err; err != nil {
		var notSent hosts.NotSentError
		if errors.As(err, &notSent) && item.Status == "pending" {
			if releaseErr := d.Registry.ReleaseHostDelivery(item.ID); releaseErr != nil {
				d.logRuntimeError("host "+host.record.Name+" delivery", releaseErr)
			}
			return
		}
		unconfirmed()
		return
	}
	submitted, body, readErr := answer.submitted, answer.body, answer.readErr
	var status hosts.StatusError
	switch {
	case errors.As(readErr, &status) && status.Code == "action_idempotency_mismatch":
		_ = d.Registry.DeadLetterUnknown(item.ID, readErr)
	case errors.As(readErr, &status) && status.Code == hosts.UnprovenCode:
		_ = d.Registry.DeadLetterMessage(item.ID, surface.DeliveryTerminal(errors.New(hosts.Unproven(host.record.Name)), surface.DeliveryHostUnproven))
	case errors.As(readErr, &status):
		_ = d.Registry.DeadLetterMessage(item.ID, fmt.Errorf("host %s refused the message: %s", host.record.Name, status.Error()))
	case readErr != nil, submitted:
		unconfirmed()
	default:
		var outcome struct {
			Result struct {
				Evidence surface.DeliveryEvidence `json:"evidence"`
				TurnID   string                   `json:"turnId"`
			} `json:"result"`
		}
		_ = json.Unmarshal(body, &outcome)
		evidence := outcome.Result.Evidence
		if evidence == "" {
			evidence = surface.EvidenceDelivered
		}
		if err := d.Registry.AckMessageWithEvidence(item.ID, item.SessionID, 0, evidence, outcome.Result.TurnID); err != nil {
			d.logRuntimeError("host "+host.record.Name+" delivery ack", err)
		}
	}
	d.invalidateDashboard()
}

type hostDeliveryAnswer struct {
	err       error
	submitted bool
	body      []byte
	readErr   error
}

func (d *Daemon) postHostDelivery(ctx context.Context, host *pairedHost, item registry.HostDelivery) hostDeliveryAnswer {
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, item.Request, http.Header{actionIdempotencyHeader: []string{item.Key}})
	if err != nil {
		return hostDeliveryAnswer{err: err}
	}
	body, readErr := hosts.ReadResponse(response)
	return hostDeliveryAnswer{submitted: response.StatusCode == http.StatusAccepted, body: body, readErr: readErr}
}

// publicSessionID renders a stored session id for this daemon's clients.
func (d *Daemon) publicSessionID(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	owner, err := d.Registry.SessionHost(sessionID)
	if err != nil || owner == "" {
		return sessionID
	}
	return hosts.Qualify(owner, sessionID)
}

type dashboardHostHealth struct {
	dashboardHost
	Detail  string              `json:"detail,omitempty"`
	Notice  string              `json:"notice,omitempty"`
	Problem string              `json:"problem,omitempty"`
	Build   *buildinfo.Identity `json:"build,omitempty"`
}

func (d *Daemon) hostSummaries() []dashboardHostHealth {
	paired := d.pairedHosts()
	result := make([]dashboardHostHealth, 0, len(paired))
	for _, host := range paired {
		state, detail := host.status()
		result = append(result, dashboardHostHealth{dashboardHost: dashboardHost{ID: host.record.ID, Name: host.record.Name, State: state}, Detail: detail, Notice: host.notice(), Problem: host.pairingProblem(), Build: host.reportedBuild()})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
