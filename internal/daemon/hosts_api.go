package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const hostPairTimeout = 20 * time.Second

type hostView struct {
	registry.PairedHost
	Detail       string `json:"detail,omitempty"`
	Notice       string `json:"notice,omitempty"`
	SessionCount int    `json:"sessionCount"`
}

func (d *Daemon) registerHostAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/host", d.apiV1Guard(dashboard, "read", d.apiHostSelfHandler))
	mux.HandleFunc("/api/v1/hosts", d.apiV1Guard(dashboard, "read", d.apiHostsHandler))
	mux.HandleFunc("/api/v1/resolve", d.apiV1Guard(dashboard, "read", d.apiResolveHandler))
}

func (d *Daemon) apiHostSelfHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost()})
	case http.MethodPut:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Renaming this host needs the settings scope.")
			return
		}
		var request struct {
			Name string `json:"name"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "A host name is required.")
			return
		}
		identity, err := d.Registry.SetHostName(request.Name)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_host_name", err.Error())
			return
		}
		d.setHostIdentity(identity)
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost()})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or PUT for this endpoint.")
	}
}

func (d *Daemon) requestHasScope(r *http.Request, scope string) bool {
	token := bearerToken(r)
	if token == "" || d.dashboard == nil || constantTokenEqual(token, d.dashboard.token) {
		return true
	}
	_, err := d.Registry.AuthenticateDevice(token, scope)
	return err == nil
}

func (d *Daemon) hostViews() ([]hostView, error) {
	records, err := d.Registry.ListHosts()
	if err != nil {
		return nil, err
	}
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, state := range snapshot.Sessions {
		if state.HostID != "" {
			counts[state.HostID]++
		}
	}
	views := make([]hostView, 0, len(records))
	for _, record := range records {
		view := hostView{PairedHost: record, SessionCount: counts[record.ID]}
		if host, found := d.pairedHost(record.ID); found {
			view.State, view.Detail = host.status()
			view.Notice = host.notice()
		}
		views = append(views, view)
	}
	return views, nil
}

func (d *Daemon) apiHostsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := d.hostViews()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "host_list_failed", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost(), "hosts": views})
	case http.MethodPost:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Pairing a host needs the settings scope.")
			return
		}
		d.addHostHandler(w, r)
	case http.MethodDelete:
		if !d.requestHasScope(r, "settings") {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Removing a host needs the settings scope.")
			return
		}
		d.removeHostHandler(w, r)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET, POST or DELETE for this endpoint.")
	}
}

func (d *Daemon) addHostHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		PairingURL string `json:"pairingUrl"`
		Address    string `json:"address"`
		Name       string `json:"name"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A pairing link is required.")
		return
	}
	link, err := hosts.ParsePairingLink(request.PairingURL)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_pairing_link", err.Error())
		return
	}
	address := strings.TrimSpace(request.Address)
	if address == "" {
		endpoint, _ := url.Parse(link.Endpoint)
		address, err = tailnetAddress(endpoint.Hostname())
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "address_required", fmt.Sprintf("Pass the host's tailnet IP with --address: %s", err))
			return
		}
	}
	if err := hosts.CheckTailnetAddress(address); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_address", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hostPairTimeout)
	defer cancel()
	token, version, err := hosts.Pair(ctx, link, address, d.hostIdentity().Name, hosts.ClientOptions{RootCAs: d.federation.rootCAs})
	if err != nil {
		var status hosts.StatusError
		if errors.As(err, &status) {
			writeAPIError(w, http.StatusBadGateway, "pairing_rejected", "The host rejected the pairing: "+status.Error())
			return
		}
		writeAPIError(w, http.StatusBadGateway, "pairing_failed", err.Error())
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" {
		name = version.HostName
	}
	d.federation.lifecycle.Lock()
	record, err := d.Registry.AddHost(registry.PairedHost{ID: version.HostID, Name: name, Endpoint: link.Endpoint, Address: address, Token: token})
	if err != nil {
		d.federation.lifecycle.Unlock()
		d.revokeRemoteDevice(token, link.Endpoint, address)
		writeAPIError(w, http.StatusConflict, "host_pairing_conflict", err.Error())
		return
	}
	if err := d.attachHostLocked(record); err != nil {
		_, _ = d.Registry.RemoveHost(record.ID)
		d.federation.lifecycle.Unlock()
		d.revokeRemoteDevice(token, link.Endpoint, address)
		writeAPIError(w, http.StatusInternalServerError, "host_attach_failed", err.Error())
		return
	}
	d.federation.lifecycle.Unlock()
	d.publishEvent("host.paired", record.ID, map[string]any{"id": record.ID, "name": record.Name})
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"host": hostView{PairedHost: record}})
}

func (d *Daemon) revokeRemoteDevice(token, endpoint, address string) {
	client, err := hosts.NewClient(endpoint, address, token, hosts.ClientOptions{RootCAs: d.federation.rootCAs})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if response, err := client.Do(ctx, http.MethodDelete, "/api/v1/device", nil, nil, nil); err == nil {
		response.Body.Close()
	}
}

func (d *Daemon) removeHostHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Host string `json:"host"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil || strings.TrimSpace(request.Host) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A host name or id is required.")
		return
	}
	d.federation.lifecycle.Lock()
	record, err := d.Registry.Host(request.Host)
	if err != nil {
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusNotFound, "host_not_found", err.Error())
		return
	}
	d.detachHostLocked(record.ID)
	d.catalog.mu.Lock()
	_, err = d.Registry.RemoveHost(record.ID)
	var flushErr error
	if err == nil {
		flushErr = d.catalog.flushCommittedLocked()
	}
	d.catalog.mu.Unlock()
	if err != nil {
		if attachErr := d.attachHostLocked(record); attachErr != nil {
			err = errors.Join(err, attachErr)
		}
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "host_remove_failed", err.Error())
		return
	}
	d.federation.lifecycle.Unlock()
	// The removal committed; a publication failure only means catalog
	// subscribers reconcile from a fresh snapshot.
	if flushErr != nil {
		d.logRuntimeError("host "+record.Name+" removal catalog", flushErr)
	}
	d.revokeRemoteDevice(record.Token, record.Endpoint, record.Address)
	d.publishEvent("host.removed", record.ID, map[string]any{"id": record.ID, "name": record.Name})
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type tailscalePeerStatus struct {
	Self struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

var tailnetAddress = func(dnsName string) (string, error) {
	path, err := tailscaleExecutable(normalizeRemoteAccessConfig(RemoteAccessConfig{}).TailscalePath)
	if err != nil {
		return "", err
	}
	output, err := runTailscale(path, "status", "--json")
	if err != nil {
		return "", err
	}
	var status tailscalePeerStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return "", err
	}
	want := strings.TrimSuffix(strings.ToLower(dnsName), ".")
	for _, peer := range status.Peer {
		if strings.TrimSuffix(strings.ToLower(peer.DNSName), ".") != want {
			continue
		}
		for _, candidate := range peer.TailscaleIPs {
			if ip := net.ParseIP(candidate); ip != nil && ip.To4() != nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("no tailnet peer named %s", dnsName)
}

type resolveMatch struct {
	ID      string              `json:"id"`
	Surface surface.SurfaceKind `json:"surface"`
	Name    string              `json:"name,omitempty"`
	Alias   string              `json:"alias,omitempty"`
	Host    dashboardHost       `json:"host"`
}

type resolveResponse struct {
	Matches []resolveMatch `json:"matches"`
	Errors  []string       `json:"errors,omitempty"`
}

// apiResolveHandler answers which sessions a target names. Without scope it
// resolves on this host only, which is what a paired daemon asks its peers;
// scope=remote fans out to every paired host instead.
func (d *Daemon) apiResolveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if target == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A target is required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.URL.Query().Get("scope") == "remote" {
		candidates := d.pairedHosts()
		if hostRef := strings.TrimSpace(r.URL.Query().Get("host")); hostRef != "" {
			host, ok := d.pairedHost(hostRef)
			if !ok {
				writeAPIError(w, http.StatusNotFound, "host_not_found", fmt.Sprintf("No paired host is named %q.", hostRef))
				return
			}
			candidates = []*pairedHost{host}
		}
		writeDashboardJSON(w, http.StatusOK, d.resolveRemote(ctx, candidates, target))
		return
	}
	matches, err := d.resolveLocal(ctx, target)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "ambiguous_target", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, resolveResponse{Matches: matches})
}

func (d *Daemon) resolveLocal(ctx context.Context, target string) ([]resolveMatch, error) {
	host := d.localHost()
	lookup := target
	if !strings.HasPrefix(target, "@") || !strings.Contains(target, "/") {
		lookup = strings.TrimPrefix(target, "@")
	}
	sid, err := d.Registry.ResolveTarget(lookup)
	if err == nil {
		session, lookupErr := d.Registry.Session(sid)
		if lookupErr != nil {
			return nil, lookupErr
		}
		alias, _ := d.Registry.ReverseAlias(sid)
		return []resolveMatch{{ID: sid, Surface: session.Surface, Name: session.Name, Alias: alias, Host: host}}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	matches := []resolveMatch{}
	for _, adapter := range d.Surfaces {
		session, resolveErr := adapter.Resolve(ctx, lookup)
		if resolveErr != nil {
			continue
		}
		alias, _ := d.Registry.ReverseAlias(session.ID)
		matches = append(matches, resolveMatch{ID: session.ID, Surface: session.Surface, Name: session.Name, Alias: alias, Host: host})
	}
	return matches, nil
}

func (d *Daemon) resolveRemote(ctx context.Context, candidates []*pairedHost, target string) resolveResponse {
	result := resolveResponse{Matches: []resolveMatch{}}
	for _, host := range candidates {
		state, _ := host.status()
		if state == hostStateConnected {
			var answer resolveResponse
			err := host.client.GetJSON(ctx, "/api/v1/resolve", url.Values{"target": []string{target}}, &answer)
			if err == nil {
				for _, match := range answer.Matches {
					match.ID = hosts.Qualify(host.record.ID, match.ID)
					match.Host = host.ref()
					result.Matches = append(result.Matches, match)
				}
				continue
			}
			var status hosts.StatusError
			if errors.As(err, &status) && status.Status == http.StatusConflict {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", host.record.Name, status.Message))
				continue
			}
		}
		result.Matches = append(result.Matches, d.resolveMirrored(host, target)...)
	}
	sort.Slice(result.Matches, func(i, j int) bool { return result.Matches[i].ID < result.Matches[j].ID })
	return result
}

func (d *Daemon) resolveMirrored(host *pairedHost, target string) []resolveMatch {
	found, err := d.Registry.MatchHostSessions(host.record.ID, target)
	if err != nil {
		return nil
	}
	matches := []resolveMatch{}
	for _, match := range found {
		matches = append(matches, resolveMatch{ID: hosts.Qualify(host.record.ID, match.ID), Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: host.ref()})
	}
	return matches
}

// hostRouted forwards a GET whose session (param) or host query names a
// paired host; everything else reaches next unchanged.
func (d *Daemon) hostRouted(param string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		var host *pairedHost
		explicitLocal := false
		if hostRef := strings.TrimSpace(query.Get("host")); hostRef != "" {
			query.Del("host")
			explicitLocal = d.isLocalHostRef(hostRef)
			if !explicitLocal {
				found, ok := d.pairedHost(hostRef)
				if !ok {
					writeAPIError(w, http.StatusNotFound, "host_not_found", fmt.Sprintf("No paired host is named %q.", hostRef))
					return
				}
				host = found
			}
			r.URL.RawQuery = query.Encode()
		}
		if value := query.Get(param); value != "" {
			routed, rawID, err := d.routeSession(value)
			if err != nil {
				writeRouteError(w, err)
				return
			}
			if routed != nil && (explicitLocal || host != nil && host != routed) {
				writeAPIError(w, http.StatusConflict, "host_mismatch", fmt.Sprintf("%s belongs to host %s, not the host this request names.", value, routed.record.Name))
				return
			}
			if routed != nil {
				host = routed
			}
			query.Set(param, rawID)
			r.URL.RawQuery = query.Encode()
		}
		if host == nil {
			next(w, r)
			return
		}
		if !writeIncompatibleHost(w, host) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "-stream") {
			d.relayHostStream(w, r, host, query)
			return
		}
		d.forwardHostRequest(w, r, host, r.Method, query, nil)
	}
}

// writeIncompatibleHost refuses to forward to a host whose federation
// version this daemon does not speak, so an older host never answers a
// request it would misread. It reports whether the request may proceed.
func writeIncompatibleHost(w http.ResponseWriter, host *pairedHost) bool {
	state, detail := host.status()
	if state != hostStateIncompatible {
		return true
	}
	writeAPIError(w, http.StatusConflict, "host_incompatible", fmt.Sprintf("Host %s needs an update: %s", host.record.Name, detail))
	return false
}

// writeRouteError answers a request routeSession refused: a qualifier naming
// this daemon on a session a paired host owns, or an unknown host.
func writeRouteError(w http.ResponseWriter, err error) {
	var owned registry.SessionOwnedError
	if errors.As(err, &owned) {
		writeAPIError(w, http.StatusConflict, "session_owned", err.Error())
		return
	}
	writeAPIError(w, http.StatusNotFound, "host_not_found", err.Error())
}

func apiPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/") {
		return path
	}
	return "/api/v1/" + strings.TrimPrefix(path, "/api/")
}

func writeHostError(w http.ResponseWriter, host *pairedHost, err error) {
	var notSent hosts.NotSentError
	if errors.As(err, &notSent) {
		writeAPIError(w, http.StatusServiceUnavailable, "host_unreachable", fmt.Sprintf("Host %s is unreachable: %s", host.record.Name, err))
		return
	}
	writeAPIError(w, http.StatusGatewayTimeout, "host_outcome_unknown", fmt.Sprintf("Host %s did not answer; the outcome is unknown: %s", host.record.Name, err))
}

func writeHostReadError(w http.ResponseWriter, host *pairedHost, err error) {
	writeAPIError(w, http.StatusServiceUnavailable, "host_unreachable", fmt.Sprintf("Host %s is unreachable: %s", host.record.Name, err))
}

func (d *Daemon) forwardHostRequest(w http.ResponseWriter, r *http.Request, host *pairedHost, method string, query url.Values, body []byte) {
	header := http.Header{}
	if key := r.Header.Get(actionIdempotencyHeader); key != "" {
		header.Set(actionIdempotencyHeader, key)
	}
	response, err := host.client.Do(r.Context(), method, apiPath(r.URL.Path), query, body, header)
	if err != nil {
		writeHostReadError(w, host, err)
		return
	}
	writeHostResponse(w, host, response)
}

func writeHostResponse(w http.ResponseWriter, host *pairedHost, response *http.Response) {
	answer, err := readHostAnswer(host, response)
	if err != nil {
		writeHostError(w, host, err)
		return
	}
	answer.write(w)
}

type hostAnswer struct {
	status  int
	header  http.Header
	payload []byte
}

// readHostAnswer reads the whole body before anything is relayed, so a
// connection lost mid-body surfaces as an error the caller can still act on.
func readHostAnswer(host *pairedHost, response *http.Response) (hostAnswer, error) {
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return hostAnswer{}, err
	}
	contentType := response.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && len(bytes.TrimSpace(payload)) > 0 {
		if qualified, qualifyErr := hosts.QualifyJSON(host.record.ID, payload); qualifyErr == nil {
			payload = qualified
		}
	}
	header := http.Header{}
	for _, key := range []string{"Content-Type", "Content-Disposition", "Cache-Control"} {
		if value := response.Header.Get(key); value != "" {
			header.Set(key, value)
		}
	}
	return hostAnswer{status: response.StatusCode, header: header, payload: payload}, nil
}

func (a hostAnswer) write(w http.ResponseWriter) {
	for key, values := range a.header {
		w.Header()[key] = values
	}
	w.WriteHeader(a.status)
	_, _ = w.Write(a.payload)
}

func (d *Daemon) relayHostStream(w http.ResponseWriter, r *http.Request, host *pairedHost, query url.Values) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	header := http.Header{"Accept": []string{"text/event-stream"}}
	if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
		header.Set("Last-Event-ID", lastID)
	}
	response, err := host.client.Open(r.Context(), apiPath(r.URL.Path), query, header)
	if err != nil {
		writeHostReadError(w, host, err)
		return
	}
	if response.StatusCode != http.StatusOK {
		writeHostResponse(w, host, response)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if data, found := strings.CutPrefix(line, "data: "); found {
			if qualified, err := hosts.QualifyJSON(host.record.ID, []byte(data)); err == nil {
				line = "data: " + string(qualified)
			}
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return
		}
		if line == "" {
			flusher.Flush()
		}
	}
}

// hostRoutedAction forwards a dashboard action whose session or host names a
// paired host. Any other session reference in the request must name the same
// host; cross-host actions are refused rather than half-applied.
func (d *Daemon) hostRoutedAction(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, actionRequestBodyLimit+1))
		r.Body.Close()
		if err != nil || len(raw) > actionRequestBodyLimit {
			http.Error(w, "invalid dashboard request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var request map[string]any
		if json.Unmarshal(raw, &request) != nil {
			next(w, r)
			return
		}
		host, rewritten, err := d.routeActionRequest(request)
		if err != nil {
			var owned registry.SessionOwnedError
			if errors.As(err, &owned) {
				writeRouteError(w, err)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if host == nil {
			if rewritten {
				raw, _ = json.Marshal(request)
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			next(w, r)
			return
		}
		if !writeIncompatibleHost(w, host) {
			return
		}
		body, _ := json.Marshal(request)
		d.forwardHostAction(w, r, host, request, body)
	}
}

var actionSessionFields = []string{"sessionId", "sourceSessionId", "targetId", "fromId", "toId"}

func (d *Daemon) routeActionRequest(request map[string]any) (*pairedHost, bool, error) {
	var host *pairedHost
	local := false
	rewritten := false
	assign := func(candidate *pairedHost) error {
		if candidate == nil {
			local = true
		} else if host != nil && host != candidate {
			return fmt.Errorf("an action cannot span hosts %s and %s", host.record.Name, candidate.record.Name)
		} else {
			host = candidate
		}
		if host != nil && local {
			return fmt.Errorf("an action cannot span this host and host %s", host.record.Name)
		}
		return nil
	}
	if hostRef, ok := request["host"].(string); ok {
		delete(request, "host")
		rewritten = true
		if hostRef = strings.TrimSpace(hostRef); hostRef != "" {
			var named *pairedHost
			if !d.isLocalHostRef(hostRef) {
				found, exists := d.pairedHost(hostRef)
				if !exists {
					return nil, false, fmt.Errorf("no paired host is named %q", hostRef)
				}
				named = found
			}
			if err := assign(named); err != nil {
				return nil, false, err
			}
		}
	}
	for _, field := range actionSessionFields {
		value, ok := request[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		routed, rawID, err := d.routeSession(value)
		if err != nil {
			return nil, false, err
		}
		if err := assign(routed); err != nil {
			return nil, false, err
		}
		if rawID != value {
			request[field] = rawID
			rewritten = true
		}
	}
	if host != nil {
		action, _ := request["action"].(string)
		if action == "session-focus" {
			return nil, false, fmt.Errorf("opening the terminal is not available for sessions on another host yet; this session runs on host %s", host.record.Name)
		}
		if strings.HasPrefix(action, "channel-") || strings.HasPrefix(action, "relay-") || strings.HasPrefix(action, "queue-") || action == "delivery-dismiss" {
			return nil, false, fmt.Errorf("%s is not available for sessions on another host yet", action)
		}
		if source, _ := request["sourceSessionId"].(string); source != "" {
			return nil, false, fmt.Errorf("a message from one session to another is not available for sessions on another host yet")
		}
	}
	return host, rewritten, nil
}

func (d *Daemon) forwardHostAction(w http.ResponseWriter, r *http.Request, host *pairedHost, request map[string]any, body []byte) {
	key := r.Header.Get(actionIdempotencyHeader)
	if key == "" {
		key = "fwd-" + uuid.NewString()
	}
	header := http.Header{actionIdempotencyHeader: []string{key}}
	response, err := host.client.Do(r.Context(), http.MethodPost, "/api/v1/actions", nil, body, header)
	if err == nil {
		answer, readErr := readHostAnswer(host, response)
		if readErr == nil {
			answer.write(w)
			return
		}
		err = readErr
	}
	action, _ := request["action"].(string)
	sessionID, _ := request["sessionId"].(string)
	if action != "send" || sessionID == "" {
		writeHostError(w, host, err)
		return
	}
	message, _ := request["message"].(string)
	var notSent hosts.NotSentError
	sent := !errors.As(err, &notSent)
	queueID, queueErr := d.Registry.QueueHostDelivery(sessionID, message, key, body, sent)
	if queueErr != nil {
		writeHostError(w, host, errors.Join(err, queueErr))
		return
	}
	d.refreshHostQueueCounts()
	receipt := delivery.Receipt{Evidence: surface.EvidenceQueued, Status: "queued", SessionID: hosts.Qualify(host.record.ID, sessionID), QueueID: queueID, Detail: fmt.Sprintf("host %s is unreachable; queued until it reconnects", host.record.Name)}
	if sent {
		receipt.Evidence = surface.EvidenceUnknown
		receipt.Status = "submitted"
		receipt.Detail = fmt.Sprintf("host %s did not answer; the outcome is unknown until it confirms this request", host.record.Name)
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": receipt})
}
