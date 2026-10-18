package daemon

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
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
	mu      sync.Mutex
	hosts   map[string]*pairedHost
	rootCAs *x509.CertPool
	ctx     context.Context
	wg      sync.WaitGroup
	running bool
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
	drain  sync.Mutex
}

func newFederation() *federation {
	return &federation{hosts: map[string]*pairedHost{}}
}

func (h *pairedHost) status() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state, h.detail
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
	d.federation.mu.Unlock()
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
	client, err := hosts.NewClient(record.Endpoint, record.Address, record.Token, hosts.ClientOptions{RootCAs: d.federation.rootCAs})
	if err != nil {
		return err
	}
	d.detachHost(record.ID)
	d.federation.mu.Lock()
	defer d.federation.mu.Unlock()
	parent := d.federation.ctx
	if parent == nil || !d.federation.running {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	host := &pairedHost{record: record, client: client, cancel: cancel, state: hostStateConnecting}
	d.federation.hosts[record.ID] = host
	if d.federation.running {
		d.federation.wg.Add(2)
		host.done.Add(2)
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

// detachHost returns once the host's mirror and delivery loops have exited,
// so no write from them can land after the caller removes the host's rows.
func (d *Daemon) detachHost(hostID string) {
	d.federation.mu.Lock()
	host, found := d.federation.hosts[hostID]
	delete(d.federation.hosts, hostID)
	d.federation.mu.Unlock()
	if found {
		host.cancel()
		host.done.Wait()
	}
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
			if owner, err := d.Registry.SessionHost(target); err == nil && owner != "" {
				return nil, "", registry.SessionOwnedError{SessionID: target, HostID: owner}
			}
			return nil, target, nil
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
	owner, err := d.Registry.SessionHost(ref)
	if err != nil || owner == "" {
		return nil, ref, nil
	}
	host, found := d.pairedHost(owner)
	if !found {
		return nil, "", fmt.Errorf("%w %q", errUnknownHost, owner)
	}
	return host, ref, nil
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
		if d.dashboard != nil {
			d.dashboard.invalidate()
		}
	}
}

func (d *Daemon) runHostMirror(ctx context.Context, host *pairedHost) {
	delay := hostRetryMin
	for ctx.Err() == nil {
		connected, err := d.mirrorHostOnce(ctx, host)
		if ctx.Err() != nil {
			return
		}
		if connected {
			delay = hostRetryMin
		}
		if err != nil {
			d.markHostDown(host, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, hostRetryMax)
	}
}

func hostFailureState(err error) string {
	var status hosts.StatusError
	var mismatch hosts.ErrProtocolMismatch
	switch {
	case errors.As(err, &status) && (status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden):
		return hostStateUnauthorized
	case errors.As(err, &mismatch), errors.Is(err, errHostIdentityChanged):
		return hostStateIncompatible
	default:
		return hostStateUnreachable
	}
}

var errHostIdentityChanged = errors.New("the daemon at this address is a different host")

func (d *Daemon) markHostDown(host *pairedHost, cause error) {
	state := hostFailureState(cause)
	detail := cause.Error()
	d.setHostState(host, state, detail, false)
	reason := hostUnavailableReason(host.record.Name, state, detail)
	rows, err := d.Registry.HostCatalogRows(host.record.ID)
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
		if err := d.applyMirroredProjection(host.record.ID, row.Session, projection, row.HostProject, row.Checkout); err != nil {
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
	HostEpoch  string             `json:"hostEpoch"`
	CatalogSeq uint64             `json:"catalogSeq"`
	Sessions   []dashboardSession `json:"sessions"`
}

func (d *Daemon) mirrorHostOnce(ctx context.Context, host *pairedHost) (bool, error) {
	version, err := host.client.Version(ctx)
	if err != nil {
		return false, err
	}
	if version.HostID != host.record.ID {
		return false, fmt.Errorf("%w (%s)", errHostIdentityChanged, version.HostID)
	}
	d.setHostState(host, hostStateConnected, "", true)
	for ctx.Err() == nil {
		seq, err := d.reconcileHost(ctx, host)
		if err != nil {
			return true, err
		}
		d.drainHostDeliveries(ctx, host)
		gap, err := d.followHostCatalog(ctx, host, seq)
		if !gap {
			return true, err
		}
	}
	return true, ctx.Err()
}

func (d *Daemon) reconcileHost(ctx context.Context, host *pairedHost) (uint64, error) {
	var snapshot remoteSnapshot
	if err := host.client.GetJSON(ctx, "/api/v1/snapshot", url.Values{"fresh": []string{"1"}}, &snapshot); err != nil {
		return 0, err
	}
	keep := map[string]struct{}{}
	for _, row := range snapshot.Sessions {
		if !servedByHost(row, host.record.ID) {
			continue
		}
		if err := d.mirrorRemoteRow(host, row); err != nil {
			d.logRuntimeError("host "+host.record.Name+" mirror "+row.ID, err)
			continue
		}
		keep[row.ID] = struct{}{}
	}
	existing, err := d.Registry.HostCatalogRows(host.record.ID)
	if err != nil {
		return 0, err
	}
	for _, row := range existing {
		if _, found := keep[row.Session.ID]; found {
			continue
		}
		if _, _, err := d.catalog.removeHostRow(host.record.ID, row.Session.ID, hosts.Qualify(host.record.ID, row.Session.ID), "omitted_by_host"); err != nil {
			return 0, err
		}
	}
	host.epoch = snapshot.HostEpoch
	if err := d.Registry.RecordHostCatalogCursor(host.record.ID, snapshot.HostEpoch, snapshot.CatalogSeq); err != nil {
		return 0, err
	}
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
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		data, found := strings.CutPrefix(scanner.Text(), "data: ")
		if !found {
			continue
		}
		var envelope catalogStreamEnvelope
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			return false, fmt.Errorf("host %s sent an invalid catalog event: %w", host.record.Name, err)
		}
		if err := d.applyHostCatalogEvent(host, envelope); err != nil {
			d.logRuntimeError("host "+host.record.Name+" catalog event", err)
		}
		if err := d.Registry.RecordHostCatalogCursor(host.record.ID, host.epoch, envelope.Seq); err != nil {
			return false, err
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("host %s catalog stream: %w", host.record.Name, err)
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
		return d.mirrorRemoteRow(host, payload.Session)
	case "session.removed":
		var payload struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		if payload.SessionID == "" || strings.Contains(payload.SessionID, hosts.Separator) {
			return nil
		}
		_, _, err := d.catalog.removeHostRow(host.record.ID, payload.SessionID, hosts.Qualify(host.record.ID, payload.SessionID), "removed_by_host")
		return err
	case "session.queue":
		var payload struct {
			SessionID  string `json:"sessionId"`
			QueueCount int    `json:"queueCount"`
		}
		if err := json.Unmarshal(envelope.Data, &payload); err != nil {
			return err
		}
		return d.updateMirroredQueueCount(host, payload.SessionID, payload.QueueCount)
	}
	return nil
}

func (d *Daemon) updateMirroredQueueCount(host *pairedHost, sessionID string, count int) error {
	rows, err := d.Registry.HostCatalogRows(host.record.ID)
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
		return d.applyMirroredProjection(host.record.ID, row.Session, projection, row.HostProject, row.Checkout)
	}
	return nil
}

func (d *Daemon) mirrorRemoteRow(host *pairedHost, row dashboardSession) error {
	session := surface.Session{ID: row.ID, Surface: row.Surface, Name: row.Name, Cwd: row.Cwd, Status: row.Status, LastActive: row.LastActive, Source: row.Source, Transport: row.Transport}
	if row.Subagent != nil {
		subagent := *row.Subagent
		session.Subagent = &subagent
		qualified := subagent
		qualified.ParentID = hosts.Qualify(host.record.ID, subagent.ParentID)
		qualified.RootID = hosts.Qualify(host.record.ID, subagent.RootID)
		row.Subagent = &qualified
	}
	hostProject, _ := json.Marshal(row.HostProject)
	checkout, _ := json.Marshal(row.Checkout)
	row.ID = hosts.Qualify(host.record.ID, row.ID)
	for index := range row.SharedWith {
		row.SharedWith[index].ID = hosts.Qualify(host.record.ID, row.SharedWith[index].ID)
	}
	if row.Runtime != nil {
		runtime := *row.Runtime
		runtime.Focusable = false
		runtime.Location = nil
		row.Runtime = &runtime
	}
	ref := host.ref()
	row.Host = &ref
	return d.applyMirroredProjection(host.record.ID, session, row, hostProject, checkout)
}

func (d *Daemon) applyMirroredProjection(hostID string, session surface.Session, projection dashboardSession, hostProject, checkout json.RawMessage) error {
	projection.ObservedAt = time.Time{}
	projection.Freshness = nil
	encoded, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	_, _, err = d.catalog.publishHostRow(hostID, registry.HostCatalogRow{Session: session, EntityID: projection.ID, Projection: encoded, HostProject: hostProject, Checkout: checkout, UnavailableReason: projection.UnavailableReason, ObservedAt: time.Now().UTC(), QueueCount: projection.QueueCount})
	return err
}

func (d *Daemon) refreshHostQueueCounts() {
	if err := d.catalog.refreshHostQueueCounts(); err != nil {
		d.logRuntimeError("host queue counts", err)
	}
}

func (h *catalogHub) refreshHostQueueCounts() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed, err := h.registry.RefreshHostQueueCounts()
	if err != nil || changed == 0 {
		return err
	}
	return h.flushCommittedLocked()
}

func (h *catalogHub) publishHostRow(hostID string, row registry.HostCatalogRow) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	event, created, err := h.registry.ApplyHostCatalogRow(hostID, row)
	if err != nil {
		return event, created, err
	}
	return event, created, h.flushCommittedLocked()
}

func (h *catalogHub) removeHostRow(hostID, sessionID, entityID, reason string) (registry.CatalogEvent, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	event, created, err := h.registry.RemoveHostCatalogRow(hostID, sessionID, entityID, reason)
	if err != nil {
		return event, created, err
	}
	return event, created, h.flushCommittedLocked()
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
			if state, _ := host.status(); state == hostStateConnected {
				d.drainHostDeliveries(ctx, host)
			}
		}
	}
}

// drainHostDeliveries replays held sends with their original key and body.
// The host's action receipts make a replay of an applied request return the
// stored outcome, so this never produces a second turn.
func (d *Daemon) drainHostDeliveries(ctx context.Context, host *pairedHost) {
	host.drain.Lock()
	defer host.drain.Unlock()
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
		if ctx.Err() != nil {
			return
		}
		d.replayHostDelivery(ctx, host, item)
	}
}

func (d *Daemon) replayHostDelivery(ctx context.Context, host *pairedHost, item registry.HostDelivery) {
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, item.Request, http.Header{actionIdempotencyHeader: []string{item.Key}})
	if err != nil {
		var notSent hosts.NotSentError
		if !errors.As(err, &notSent) {
			_ = d.Registry.MarkHostDeliverySent(item.ID)
		}
		return
	}
	if err := d.Registry.MarkHostDeliverySent(item.ID); err != nil {
		d.logRuntimeError("host "+host.record.Name+" delivery", err)
		return
	}
	submitted := response.StatusCode == http.StatusAccepted
	body, readErr := hosts.ReadResponse(response)
	var status hosts.StatusError
	switch {
	case errors.As(readErr, &status) && status.Code == "action_idempotency_mismatch":
		_ = d.Registry.DeadLetterUnknown(item.ID, readErr)
	case errors.As(readErr, &status):
		_, _ = d.Registry.DeadLetterMessage(item.ID, fmt.Errorf("host %s refused the message: %s", host.record.Name, status.Error()))
	case readErr != nil:
	case submitted:
		if time.Since(item.QueuedAt) > hostDeliveryPatience {
			_ = d.Registry.DeadLetterUnknown(item.ID, fmt.Errorf("host %s has not confirmed the message", host.record.Name))
		}
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
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
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
	Detail string `json:"detail,omitempty"`
}

func (d *Daemon) hostSummaries() []dashboardHostHealth {
	paired := d.pairedHosts()
	result := make([]dashboardHostHealth, 0, len(paired))
	for _, host := range paired {
		state, detail := host.status()
		result = append(result, dashboardHostHealth{dashboardHost: dashboardHost{ID: host.record.ID, Name: host.record.Name, State: state}, Detail: detail})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
