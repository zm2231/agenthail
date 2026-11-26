package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/simhost"
)

const simulatorFileLimit = 8 << 20

type simulatorForwardRequest struct {
	Owner   string          `json:"owner"`
	Request simhost.Request `json:"request"`
}

func (d *Daemon) registerSimulatorRoutes(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/simulator", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiSimulatorHandler)))
	mux.HandleFunc("/api/v1/simulator/install", d.apiV1Guard(dashboard, "control", d.hostOnly(d.apiSimulatorInstallHandler)))
	mux.HandleFunc("/api/v1/simulator/status", d.apiV1Guard(dashboard, "read", d.hostOnly(d.apiSimulatorStatusHandler)))
}

type simulatorStatusResponse struct {
	Simulators bool `json:"simulators"`
}

func (d *Daemon) apiSimulatorStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	ready := d.simHost != nil && d.simHost.Local() && d.simulatorStatus(r.Context()).Ready
	writeDashboardJSON(w, http.StatusOK, simulatorStatusResponse{Simulators: ready})
}

// knownSimulators is whether this host runs simulators by its last probe,
// without waiting on one: nil until a first probe ends. A missing or stale
// status starts a probe.
func (d *Daemon) knownSimulators() *bool {
	if d.simHost == nil || !d.simHost.Local() {
		return new(false)
	}
	d.probeSimulators()
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	if d.simStatusAt.IsZero() {
		return nil
	}
	return new(d.simStatus.Ready)
}

func (d *Daemon) apiSimulatorHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	hostID, ok := requestingHost(r.Context())
	if !ok || hostID == "" {
		writeAPIError(w, http.StatusForbidden, "host_only", "Only a paired host forwards simulator calls.")
		return
	}
	var req simulatorForwardRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, apiRequestBodyLimit)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if !d.servesForwardedSimulator(w, r, req.Owner) {
		return
	}
	if req.Request.Action == "install" {
		writeAPIError(w, http.StatusConflict, "invalid", "A session on another host installs by uploading its app.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), simulatorTimeout(req.Request.Action))
	defer cancel()
	out, err := d.simHost.Call(ctx, hosts.Qualify(hostID, req.Owner), req.Request)
	if err != nil {
		refusal := mcpRefusalFor(simulatorRefusal(err))
		writeAPIError(w, http.StatusConflict, refusal.Code, refusal.Message)
		return
	}
	if req.Request.Action == "release" {
		d.simHost.RemoveIdleOwner(hosts.Qualify(hostID, req.Owner))
	}
	if lease, ok := out["lease"].(simhost.LeaseView); ok {
		lease.OutDir = ""
		out["lease"] = lease
	}
	delete(out, "outDir")
	if file, _ := out["file"].(string); file != "" {
		data, err := readBounded(file, simulatorFileLimit)
		if err != nil {
			writeAPIError(w, http.StatusConflict, "too_large", err.Error())
			return
		}
		out["fileData"] = base64.StdEncoding.EncodeToString(data)
		name := filepath.Base(file)
		if _, rest, found := strings.Cut(name, "-"); found {
			name = rest
		}
		out["fileName"] = name
		delete(out, "file")
	}
	writeDashboardJSON(w, http.StatusOK, out)
}

func (d *Daemon) servesForwardedSimulator(w http.ResponseWriter, r *http.Request, owner string) bool {
	if owner == "" || len(owner) > 256 || strings.Contains(owner, "::") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "owner must be the calling session's id.")
		return false
	}
	if d.simHost == nil || !d.simHost.Local() {
		writeAPIError(w, http.StatusConflict, "unavailable", "This host runs no iOS simulators.")
		return false
	}
	if status := d.simulatorStatus(r.Context()); !status.Ready {
		writeAPIError(w, http.StatusConflict, "unavailable", "This host can't run iOS simulators: "+status.Problem+".")
		return false
	}
	return true
}

func (d *Daemon) apiSimulatorInstallHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	hostID, ok := requestingHost(r.Context())
	if !ok || hostID == "" {
		writeAPIError(w, http.StatusForbidden, "host_only", "Only a paired host uploads apps to install.")
		return
	}
	query := r.URL.Query()
	if !d.servesForwardedSimulator(w, r, query.Get("owner")) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), simulatorTimeout("install"))
	defer cancel()
	out, err := d.simHost.InstallArchive(ctx, hosts.Qualify(hostID, query.Get("owner")), simhost.Request{Action: "install", Kind: query.Get("kind"), Runtime: query.Get("runtime")}, r.Body)
	if err != nil {
		refusal := mcpRefusalFor(simulatorRefusal(err))
		writeAPIError(w, http.StatusConflict, refusal.Code, refusal.Message)
		return
	}
	writeDashboardJSON(w, http.StatusOK, out)
}

func readBounded(path string, limit int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("the result is over %d MiB", limit>>20)
	}
	return os.ReadFile(path)
}

func simulatorTimeout(simulatorAction string) time.Duration {
	switch simulatorAction {
	case "acquire":
		return 11 * time.Minute
	case "install":
		return 10 * time.Minute
	}
	return time.Minute
}

func (d *Daemon) simulatorDeviceHost(ctx context.Context) (*pairedHost, error) {
	config, err := dashconfig.Load()
	if err != nil {
		return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("%s can't be read, so the simulator host it may name is unknown: %v", dashconfig.Path(), err)}
	}
	if name := strings.TrimSpace(config.SimulatorHost); name != "" {
		host, found := d.pairedHost(name)
		if !found {
			return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("simulatorHost in %s names %s, which is not a paired host.", dashconfig.Path(), name)}
		}
		return host, nil
	}
	var connected []*pairedHost
	for _, host := range d.pairedHosts() {
		if state, _ := host.status(); state == hostStateConnected {
			connected = append(connected, host)
		}
	}
	var wg sync.WaitGroup
	for _, host := range connected {
		if host.simulatorsStale(time.Minute) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d.refreshSimulators(ctx, host)
			}()
		}
	}
	wg.Wait()
	var capable []*pairedHost
	for _, host := range connected {
		if host.runsSimulators() {
			capable = append(capable, host)
		}
	}
	switch len(capable) {
	case 1:
		return capable[0], nil
	case 0:
		return nil, &mcpRefusal{Code: "unavailable", Message: "This host runs no iOS simulators, and no connected paired Mac does.", Retryable: true}
	default:
		names := make([]string, 0, len(capable))
		for _, host := range capable {
			names = append(names, host.record.Name)
		}
		return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("Several paired Macs run simulators (%s); the person picks one with simulatorHost in %s.", strings.Join(names, ", "), dashconfig.Path())}
	}
}

func (d *Daemon) refreshSimulators(ctx context.Context, host *pairedHost) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var status simulatorStatusResponse
	if err := d.ledgerHostCall(ctx, host, http.MethodGet, "/api/v1/simulator/status", nil, nil, &status); err == nil {
		host.setSimulators(status.Simulators)
	}
}

func (d *Daemon) forwardSimulator(ctx context.Context, owner string, req simhost.Request) (map[string]any, error) {
	var bundle *simhost.AppBundle
	if req.Action == "install" {
		var err error
		if bundle, err = simhost.OpenApp(req.App); err != nil {
			return nil, simulatorRefusal(err)
		}
		defer bundle.Close()
	}
	host, err := d.simulatorDeviceHost(ctx)
	if err != nil {
		return nil, err
	}
	d.noteSimulatorHost(owner, host.record.ID)
	ctx, cancel := context.WithTimeout(ctx, simulatorTimeout(req.Action)+10*time.Second)
	defer cancel()
	if bundle != nil {
		out, err := uploadApp(ctx, host, owner, req, bundle)
		if err != nil {
			return nil, forwardRefusal(host, err)
		}
		out["installed"] = req.App
		out["host"] = host.record.Name
		return out, nil
	}
	var out map[string]any
	if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/simulator", nil, simulatorForwardRequest{Owner: owner, Request: req}, &out); err != nil {
		return nil, forwardRefusal(host, err)
	}
	out["host"] = host.record.Name
	if encoded, _ := out["fileData"].(string); encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, err
		}
		name, _ := out["fileName"].(string)
		file, err := d.simHost.WriteOwnerFile(owner, filepath.Base(strings.TrimPrefix(name, ".")), data)
		if err != nil {
			return nil, simulatorRefusal(err)
		}
		out["file"] = file
		delete(out, "fileData")
		delete(out, "fileName")
	}
	return out, nil
}

func forwardRefusal(host *pairedHost, err error) error {
	var status hosts.StatusError
	if errors.As(err, &status) && status.Code != "" {
		return &mcpRefusal{Code: status.Code, Message: status.Message + " (on " + host.record.Name + ")"}
	}
	var refusal *simhost.Error
	if errors.As(err, &refusal) {
		return simulatorRefusal(refusal)
	}
	return &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("%s did not answer: %v", host.record.Name, err), Retryable: true}
}

// Expect: 100-continue holds the body until the Mac has checked the lease.
func uploadApp(ctx context.Context, host *pairedHost, owner string, req simhost.Request, bundle *simhost.AppBundle) (map[string]any, error) {
	reader, writer := io.Pipe()
	sent := make(chan error, 1)
	go func() {
		err := bundle.WriteArchive(writer)
		writer.CloseWithError(err)
		sent <- err
	}()
	query := url.Values{"owner": []string{owner}, "kind": []string{req.Kind}, "runtime": []string{req.Runtime}}
	response, err := host.client.Stream(ctx, "/api/v1/simulator/install", query, reader, http.Header{"Content-Type": []string{"application/gzip"}, "Expect": []string{"100-continue"}})
	reader.CloseWithError(errors.New("upload ended"))
	if err != nil {
		var refusal *simhost.Error
		if sendErr := <-sent; errors.As(sendErr, &refusal) {
			return nil, refusal
		}
		return nil, err
	}
	body, err := hosts.ReadResponse(response)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *Daemon) noteSimulatorHost(owner, hostID string) {
	d.simOwnersMu.Lock()
	defer d.simOwnersMu.Unlock()
	if d.simForwarded == nil {
		d.simForwarded = map[string]map[string]bool{}
	}
	if d.simForwarded[owner] == nil {
		d.simForwarded[owner] = map[string]bool{}
	}
	d.simForwarded[owner][hostID] = true
}

func (d *Daemon) releaseForwardedSimulators(owner string) {
	d.simOwnersMu.Lock()
	hostIDs := d.simForwarded[owner]
	delete(d.simForwarded, owner)
	d.simOwnersMu.Unlock()
	for hostID := range hostIDs {
		host, found := d.pairedHost(hostID)
		if !found {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/simulator", nil, simulatorForwardRequest{Owner: owner, Request: simhost.Request{Action: "release"}}, nil); err != nil {
			log.Printf("release the simulators %s holds on %s: %v", owner, host.record.Name, err)
		}
		cancel()
	}
}
