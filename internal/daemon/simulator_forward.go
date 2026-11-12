package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
}

// runsSimulators answers from the last status after the first probe, so /api/v1/version waits on xcrun at most once.
func (d *Daemon) runsSimulators(ctx context.Context) bool {
	if d.simHost == nil || !d.simHost.Local() {
		return false
	}
	d.simStatusMu.Lock()
	ready, never, stale := d.simStatus.Ready, d.simStatusAt.IsZero(), time.Since(d.simStatusAt) >= time.Minute
	d.simStatusMu.Unlock()
	if never {
		return d.simulatorStatus(ctx).Ready
	}
	if stale {
		go d.simulatorStatus(context.WithoutCancel(ctx))
	}
	return ready
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
	if req.Owner == "" || len(req.Owner) > 256 || strings.Contains(req.Owner, "::") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "owner must be the calling session's id.")
		return
	}
	if d.simHost == nil || !d.simHost.Local() {
		writeAPIError(w, http.StatusConflict, "unavailable", "This host runs no iOS simulators.")
		return
	}
	if status := d.simulatorStatus(r.Context()); !status.Ready {
		writeAPIError(w, http.StatusConflict, "unavailable", "This host can't run iOS simulators: "+status.Problem+".")
		return
	}
	if req.Request.Action == "install" {
		writeAPIError(w, http.StatusConflict, "unavailable", "A session on another host can't install apps here yet; launch one already on the simulator.")
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

func simulatorTimeout(action string) time.Duration {
	if action == "acquire" {
		return 11 * time.Minute
	}
	return time.Minute
}

func (d *Daemon) simulatorDeviceHost() (*pairedHost, error) {
	config, _ := LoadDashboardConfig()
	if name := strings.TrimSpace(config.SimulatorHost); name != "" {
		host, found := d.pairedHost(name)
		if !found {
			return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("simulatorHost in %s names %s, which is not a paired host.", DashboardConfigPath(), name)}
		}
		return host, nil
	}
	var capable []*pairedHost
	for _, host := range d.pairedHosts() {
		if state, _ := host.status(); state == hostStateConnected && host.runsSimulators() {
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
		return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("Several paired Macs run simulators (%s); the person picks one with simulatorHost in %s.", strings.Join(names, ", "), DashboardConfigPath())}
	}
}

func (d *Daemon) forwardSimulator(ctx context.Context, owner string, req simhost.Request) (map[string]any, error) {
	if req.Action == "install" {
		return nil, refuse("unavailable", "A session on another host can't install apps on the Mac's simulator yet; launch one already on it.")
	}
	host, err := d.simulatorDeviceHost()
	if err != nil {
		return nil, err
	}
	d.noteSimulatorHost(owner, host.record.ID)
	ctx, cancel := context.WithTimeout(ctx, simulatorTimeout(req.Action)+10*time.Second)
	defer cancel()
	var out map[string]any
	if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/simulator", nil, simulatorForwardRequest{Owner: owner, Request: req}, &out); err != nil {
		var status hosts.StatusError
		if errors.As(err, &status) && status.Code != "" {
			return nil, &mcpRefusal{Code: status.Code, Message: status.Message + " (on " + host.record.Name + ")"}
		}
		return nil, &mcpRefusal{Code: "unavailable", Message: fmt.Sprintf("%s did not answer: %v", host.record.Name, err), Retryable: true}
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

func (d *Daemon) noteSimulatorHost(owner, hostID string) {
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	if d.simForwarded == nil {
		d.simForwarded = map[string]string{}
	}
	d.simForwarded[owner] = hostID
}

func (d *Daemon) releaseForwardedSimulators(owner string) {
	d.simStatusMu.Lock()
	hostID := d.simForwarded[owner]
	delete(d.simForwarded, owner)
	d.simStatusMu.Unlock()
	if hostID == "" {
		return
	}
	host, found := d.pairedHost(hostID)
	if !found {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := d.ledgerHostCall(ctx, host, http.MethodPost, "/api/v1/simulator", nil, simulatorForwardRequest{Owner: owner, Request: simhost.Request{Action: "release"}}, nil); err != nil {
		log.Printf("release the simulators %s holds on %s: %v", owner, host.record.Name, err)
	}
}
