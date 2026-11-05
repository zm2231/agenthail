package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

const hostPairTimeout = 20 * time.Second

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
	token, version, err := hosts.Pair(ctx, link, address, d.hostIdentity().Name, d.hostIdentity().ID, hosts.ClientOptions{RootCAs: d.federation.rootCAs})
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
	if existing, err := d.Registry.Host(version.HostID); err == nil && existing.ID == version.HostID {
		d.repairHostLocked(w, existing, strings.ToLower(strings.TrimSpace(request.Name)), link.Endpoint, address, token)
		return
	}
	record, err := d.Registry.AddHost(registry.PairedHost{ID: version.HostID, Name: name, Endpoint: link.Endpoint, Address: address, Token: token})
	if err != nil {
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusConflict, "host_pairing_conflict", d.discardPairing(err.Error(), version.HostName, token, link.Endpoint, address))
		return
	}
	if err := d.attachHostLocked(record); err != nil {
		_, _ = d.Registry.RemoveHost(record.ID)
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "host_attach_failed", d.discardPairing(err.Error(), version.HostName, token, link.Endpoint, address))
		return
	}
	d.federation.lifecycle.Unlock()
	d.publishEvent("host.paired", record.ID, map[string]any{"id": record.ID, "name": record.Name})
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"host": hostView{PairedHost: record}})
}

// repairHostLocked pairs an already-paired host again in place: its sessions,
// aliases, routes, held sends and ledger sync keep its host id, and only the
// token changes. The token it replaces is revoked on the host. The caller
// holds federation.lifecycle; it is released here.
func (d *Daemon) repairHostLocked(w http.ResponseWriter, existing registry.PairedHost, name, endpoint, address, token string) {
	if name != "" && name != existing.Name {
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusConflict, "host_pairing_conflict", d.discardPairing(fmt.Sprintf("host %s is already paired as %s; pair it again without --name", existing.ID, existing.Name), existing.Name, token, endpoint, address))
		return
	}
	record, previous, err := d.Registry.RepairHost(registry.PairedHost{ID: existing.ID, Endpoint: endpoint, Address: address, Token: token})
	if err != nil {
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "host_repair_failed", d.discardPairing(err.Error(), existing.Name, token, endpoint, address))
		return
	}
	if err := d.attachHostLocked(record); err != nil {
		if _, _, restoreErr := d.Registry.RepairHost(previous); restoreErr == nil {
			err = errors.Join(err, d.attachHostLocked(previous))
		} else {
			err = errors.Join(err, restoreErr)
		}
		d.federation.lifecycle.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "host_attach_failed", d.discardPairing(err.Error(), existing.Name, token, endpoint, address))
		return
	}
	d.federation.lifecycle.Unlock()
	view := hostView{PairedHost: record}
	if err := d.revokeRemoteDevice(previous.Token, previous.Endpoint, previous.Address); err != nil {
		view.Notice = unrevokedToken("the token this host held before", record.Name, err)
	}
	d.publishEvent("host.paired", record.ID, map[string]any{"id": record.ID, "name": record.Name})
	writeDashboardJSON(w, http.StatusOK, map[string]any{"host": view, "repaired": true})
}

// discardPairing revokes a token a pairing just redeemed and could not keep,
// and adds to message how to remove it by hand when the host refuses.
func (d *Daemon) discardPairing(message, hostName, token, endpoint, address string) string {
	if err := d.revokeRemoteDevice(token, endpoint, address); err != nil {
		return message + "; " + unrevokedToken("the token this pairing was just given", hostName, err)
	}
	return message
}

func unrevokedToken(which, hostName string, err error) string {
	return fmt.Sprintf("%s could not be revoked on %s: %s; find it there with 'agenthail devices list' and run 'agenthail devices remove <id>'", which, hostName, err)
}

func (d *Daemon) revokeRemoteDevice(token, endpoint, address string) error {
	client, err := hosts.NewClient(endpoint, address, token, hosts.ClientOptions{RootCAs: d.federation.rootCAs})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.RevokeDevice(ctx)
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
	if flushErr != nil {
		d.logRuntimeError("host "+record.Name+" removal catalog", flushErr)
	}
	d.revokeRemoteDevice(record.Token, record.Endpoint, record.Address)
	d.publishEvent("host.removed", record.ID, map[string]any{"id": record.ID, "name": record.Name})
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
}
