package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

type resolveMatch struct {
	ID      string              `json:"id"`
	Surface surface.SurfaceKind `json:"surface"`
	Name    string              `json:"name,omitempty"`
	Alias   string              `json:"alias,omitempty"`
	Host    dashboardHost       `json:"host"`
	Exact   bool                `json:"exact"`
}

type resolveResponse struct {
	Matches []resolveMatch `json:"matches"`
	Errors  []string       `json:"errors,omitempty"`
}

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
	named := strings.HasPrefix(target, "@") && !strings.Contains(target, "/")
	resolve := d.Registry.ResolveTarget
	if named {
		resolve = func(name string) (string, error) { return d.Registry.ExactTarget("", name) }
	}
	sid, err := resolve(lookup)
	if err == nil {
		session, lookupErr := d.Registry.Session(sid)
		if lookupErr != nil {
			return nil, lookupErr
		}
		alias, _ := d.Registry.ReverseAlias(sid)
		return []resolveMatch{{ID: sid, Surface: session.Surface, Name: session.Name, Alias: alias, Host: host, Exact: named || d.Registry.NamesSession(*session, target)}}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	matches := []resolveMatch{}
	if named {
		return matches, nil
	}
	for _, adapter := range d.Surfaces {
		session, resolveErr := adapter.Resolve(ctx, lookup)
		if resolveErr != nil {
			continue
		}
		alias, _ := d.Registry.ReverseAlias(session.ID)
		matches = append(matches, resolveMatch{ID: session.ID, Surface: session.Surface, Name: session.Name, Alias: alias, Host: host, Exact: d.Registry.NamesSession(*session, lookup)})
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
		matches = append(matches, resolveMatch{ID: hosts.Qualify(host.record.ID, match.ID), Surface: match.Surface, Name: match.Name, Alias: match.Alias, Host: host.ref(), Exact: true})
	}
	return matches
}
