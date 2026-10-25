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
	client.RevokeDevice(ctx)
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
