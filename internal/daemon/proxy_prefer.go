package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const proxyPreferTimeout = 30 * time.Second

// Host tokens carry no settings scope; the host that forwards a request
// already required settings from its own client.
func (d *Daemon) proxyPreferGuard(dashboard *dashboardServer) http.HandlerFunc {
	handler := apiV1JSONHandler(d.proxyPreferHandler)
	fromClient := d.apiV1Guard(dashboard, "settings", handler)
	fromHost := d.apiV1Guard(dashboard, registry.HostScope, handler)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("forwarded") == "1" {
			fromHost(w, r)
			return
		}
		fromClient(w, r)
	}
}

func (d *Daemon) proxyPreferHandler(w http.ResponseWriter, r *http.Request) {
	request, body, ok := readProxyPreferRequest(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("forwarded") == "1" {
		d.proxyPreferHere(w, r, request)
		return
	}
	sets := d.proxySetsEverywhere(r.Context(), time.Now())
	index := slices.IndexFunc(sets, func(set usageSet) bool {
		return set.Provider == "proxy" && set.Proxy != nil && set.Proxy.ID == request.Proxy
	})
	if index < 0 {
		writeAPIError(w, http.StatusNotFound, "no_proxy", fmt.Sprintf("No host knows a proxy %q.", request.Proxy))
		return
	}
	set := sets[index]
	if set.ProxyProblem != "" {
		writeAPIError(w, http.StatusConflict, "proxy_unreadable", set.Problem+" Changing its preference needs a host that can read it.")
		return
	}
	var host *pairedHost
	if !set.Local {
		found, ok := d.pairedHost(set.HostID)
		if !ok {
			writeAPIError(w, http.StatusServiceUnavailable, "host_unreachable", fmt.Sprintf("Host %s, which holds the %s proxy's key, is not connected.", set.Host, set.Name))
			return
		}
		host = found
	}
	if r.Method == http.MethodPost {
		if host == nil {
			d.proxyPreferHere(w, r, request)
		} else {
			d.forwardHostRequest(w, r, host, http.MethodPost, url.Values{"forwarded": {"1"}}, body)
		}
		return
	}
	plan, answered := d.proxyPreferPlanFrom(w, r, host, request)
	if !answered {
		return
	}
	hosts, err := d.proxyReach(r.Context(), sets, request.Proxy)
	plan.Hosts = hosts
	if err != nil {
		plan.Warnings = append(plan.Warnings, "Running sessions could not be listed: "+err.Error())
	}
	plan.confirm()
	writeDashboardJSON(w, http.StatusOK, plan)
}

func readProxyPreferRequest(w http.ResponseWriter, r *http.Request) (proxyPreferRequest, []byte, bool) {
	var request proxyPreferRequest
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		request = proxyPreferRequest{Proxy: query.Get("proxy"), Account: query.Get("account"), Set: query.Get("set"), Now: query.Get("now") == "true" || query.Get("now") == "1"}
		return request, nil, true
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, apiRequestBodyLimit))
		if err != nil || json.Unmarshal(body, &request) != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "Send {proxy, account, set, now, planId}.")
			return request, nil, false
		}
		if request.PlanID == "" {
			writeAPIError(w, http.StatusBadRequest, "plan_required", "Preview the change first and send its planId.")
			return request, nil, false
		}
		return request, body, true
	}
	writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET to preview and POST to apply.")
	return request, nil, false
}

func (d *Daemon) proxyPreferPlanFrom(w http.ResponseWriter, r *http.Request, host *pairedHost, request proxyPreferRequest) (proxyPreferPlan, bool) {
	if host == nil {
		config, ok := d.localProxyConfig(request.Proxy)
		if !ok {
			writeAPIError(w, http.StatusNotFound, "no_proxy", fmt.Sprintf("No proxy %q on this host.", request.Proxy))
			return proxyPreferPlan{}, false
		}
		plan, err := d.proxyPreferPlanHere(r.Context(), config, request)
		if err != nil {
			writeProxyPlanError(w, err)
			return plan, false
		}
		return plan, true
	}
	query := url.Values{"forwarded": {"1"}, "proxy": {request.Proxy}, "account": {request.Account}, "set": {request.Set}, "now": {fmt.Sprint(request.Now)}}
	response, err := host.client.Do(r.Context(), http.MethodGet, "/api/v1/proxies/prefer", query, nil, nil)
	if err != nil {
		writeHostReadError(w, host, err)
		return proxyPreferPlan{}, false
	}
	answer, err := readHostAnswer(host, response)
	if err != nil {
		writeHostError(w, host, err)
		return proxyPreferPlan{}, false
	}
	var plan proxyPreferPlan
	if answer.status != http.StatusOK || json.Unmarshal(answer.payload, &plan) != nil {
		answer.write(w)
		return plan, false
	}
	return plan, true
}

func (d *Daemon) proxyPreferHere(w http.ResponseWriter, r *http.Request, request proxyPreferRequest) {
	config, ok := d.localProxyConfig(request.Proxy)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "no_proxy", fmt.Sprintf("No proxy %q on this host.", request.Proxy))
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), proxyPreferTimeout)
	defer cancel()
	entry := d.proxyUsage.entry(config)
	if r.Method == http.MethodPost {
		entry.writing.Lock()
		defer entry.writing.Unlock()
	}
	plan, err := d.proxyPreferPlanHere(ctx, config, request)
	if err != nil {
		writeProxyPlanError(w, err)
		return
	}
	if r.Method != http.MethodPost {
		writeDashboardJSON(w, http.StatusOK, plan)
		return
	}
	if request.PlanID != plan.PlanID {
		writeAPIError(w, http.StatusConflict, "plan_stale", "The proxy's preferences changed since you confirmed; review again.")
		return
	}
	if plan.Nothing != "" {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"done": []string{}, "message": plan.Nothing})
		return
	}
	done, err := d.applyProxyPrefer(ctx, config, entry, plan)
	d.proxyUsage.fresh(ctx, config)
	if err != nil {
		d.log.Printf("proxy %s: %s %s now=%v failed: %s", config.Name, plan.Set, plan.Account.Label, plan.Now, err)
		writeAPIErrorWith(w, http.StatusBadGateway, "proxy_write_failed", err.Error(), map[string]any{"done": done})
		return
	}
	d.log.Printf("proxy %s: %s %s now=%v: %s", config.Name, plan.Set, plan.Account.Label, plan.Now, strings.Join(done, "; "))
	writeDashboardJSON(w, http.StatusOK, map[string]any{"done": done, "message": strings.Join(done, "; ") + "."})
}

func (d *Daemon) proxyPreferPlanHere(ctx context.Context, config proxyacct.Config, request proxyPreferRequest) (proxyPreferPlan, error) {
	reading := d.proxyUsage.fresh(ctx, config)
	if reading.err != nil {
		problem, _ := proxyProblemText(config, reading.err)
		return proxyPreferPlan{}, &proxyPlanError{http.StatusConflict, string(proxyacct.ProblemOf(reading.err)), problem}
	}
	plan, err := planProxyPrefer(reading.accounts, request)
	if err != nil {
		return plan, err
	}
	plan.Proxy, plan.Host = d.proxyIdentity(config), d.localHost().Name
	return plan, nil
}

func writeProxyPlanError(w http.ResponseWriter, err error) {
	var planErr *proxyPlanError
	if errors.As(err, &planErr) {
		writeAPIError(w, planErr.status, planErr.code, planErr.err)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func (d *Daemon) localProxyConfig(id string) (proxyacct.Config, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return proxyacct.Config{}, false
	}
	configs, _ := surface.ConfiguredProxies(home)
	for _, config := range configs {
		if d.proxyIdentity(config).ID == id {
			return config, true
		}
	}
	return proxyacct.Config{}, false
}
