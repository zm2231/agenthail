package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

func (a *App) cmdClaudeRuns(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail runs [--json]")
	}
	adapter := a.adapters().ForSurface(surface.KindClaude)
	observer, ok := adapter.(surface.ClaudeRunObserver)
	if !ok {
		return fmt.Errorf("Claude run observation is unavailable")
	}
	runs, err := observer.ObserveClaudeRuns(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude job records: %w", err)
	}
	links, err := observer.ObserveAllClaudeSubagents(context.Background())
	if err != nil {
		return fmt.Errorf("read Claude subagent records: %w", err)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"runs": runs, "subagents": links})
	}
	for _, run := range runs {
		fmt.Printf("%s\t%s\t%s\t%s\n", run.JobID, run.ProviderState, run.SessionID, run.RunType)
	}
	for _, link := range links {
		fmt.Printf("subagent\t%s\t%s\t%s\n", link.ParentSessionID, link.AgentID, link.TranscriptPath)
	}
	return nil
}

func (a *App) cmdList(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail list [--all|--archived] [--project <name|id>|--cwd <path>] [--wide] [--json]")
	}
	scope, err := listScopeFromArgs(args)
	if err != nil {
		return err
	}
	projectSelector := strings.TrimSpace(flagVal(args, "--project"))
	jsonOut := hasFlag(args, "--json")
	rootCwd := flagVal(args, "--cwd")
	if rootCwd != "" && projectSelector != "" {
		return fmt.Errorf("choose either --cwd or --project")
	}
	ctx := context.Background()

	allSessions := make([]surface.Session, 0)
	surfaceErrors := map[string]string{}
	catalog, catalogBacked, catalogErr := a.listCatalogSnapshot()
	if catalogErr != nil {
		if jsonOut {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"sessions": allSessions, "errors": map[string]string{"catalog": catalogErr.Error()}}); err != nil {
				return err
			}
		}
		return catalogErr
	}
	successfulSurfaces := 0
	remoteRows := map[string]listHostRow{}
	views := map[string]listCatalogView{}
	if !catalogBacked && (projectSelector != "" || rootCwd != "" || hasFlag(args, "--archived")) {
		return fmt.Errorf("--project, --cwd and --archived need the daemon; start it with: agenthail daemon start")
	}
	if catalogBacked {
		successfulSurfaces = 1
		for _, record := range catalog.Sessions {
			if record.HostID != "" {
				session, row := listRemoteSession(record)
				session.Subagents = catalogSubagentRollup(record)
				remoteRows[session.ID] = row
				views[session.ID] = catalogListView(record)
				session.Holders = views[session.ID].Holders
				allSessions = append(allSessions, session)
				continue
			}
			views[record.Session.ID] = catalogListView(record)
			session, err := normalizeSessionCwd(record.Session)
			if err != nil {
				surfaceErrors[string(record.Session.Surface)] = fmt.Sprintf("normalize session workspace: %s", err)
				continue
			}
			session.Subagents = catalogSubagentRollup(record)
			session.Holders = views[session.ID].Holders
			allSessions = append(allSessions, session)
		}
		for _, record := range catalog.Surfaces {
			if record.Health != "healthy" {
				detail := record.Detail
				if detail == "" {
					detail = "catalog health is " + record.Health
				}
				surfaceErrors[string(record.Surface)] = detail
			}
		}
	} else {
		for _, s := range a.allSurfaces() {
			sessions, err := s.List(ctx)
			if err != nil {
				surfaceErrors[string(s.Name())] = err.Error()
				continue
			}
			successfulSurfaces++
			for _, sess := range sessions {
				var normalizeErr error
				sess, normalizeErr = normalizeSessionCwd(sess)
				if normalizeErr != nil {
					surfaceErrors[string(s.Name())] = fmt.Sprintf("normalize session workspace: %s", normalizeErr)
					continue
				}
				if a.registry() != nil {
					if err := a.registry().RegisterSession(sess); err != nil {
						surfaceErrors[string(s.Name())] = fmt.Sprintf("register session: %s", err)
						continue
					}
				}
				allSessions = append(allSessions, sess)
			}
		}
	}

	aliased := make(map[string]string)
	if a.registry() != nil {
		rows, _ := a.registry().ListAliases()
		for _, r := range rows {
			aliased[r.SessionID] = r.Name
		}
	}
	for id, row := range remoteRows {
		if row.Alias != "" {
			aliased[id] = row.Alias
		}
	}
	var localHost *hostRef
	pairedHosts := 0
	if a.registry() != nil {
		identity, err := a.registry().HostIdentity()
		if err != nil {
			return fmt.Errorf("read host identity: %w", err)
		}
		localHost = &hostRef{ID: identity.ID, Name: identity.Name, State: "local"}
		paired, err := a.registry().ListHosts()
		if err != nil {
			return fmt.Errorf("list paired hosts: %w", err)
		}
		pairedHosts = len(paired)
	}

	showAll := hasFlag(args, "--all")
	if showAll && a.registry() != nil {
		stored, err := a.registry().ListSessions(0)
		if err != nil {
			return fmt.Errorf("list saved sessions: %w", err)
		}
		seen := make(map[string]bool, len(allSessions))
		for _, session := range allSessions {
			seen[session.ID] = true
		}
		for _, session := range stored {
			var normalizeErr error
			session, normalizeErr = normalizeSessionCwd(session)
			if normalizeErr != nil {
				return fmt.Errorf("normalize saved session workspace: %w", normalizeErr)
			}
			if !seen[session.ID] {
				allSessions = append(allSessions, session)
			}
		}
	}
	if !showAll && !catalogBacked {
		cutoff := time.Now().AddDate(0, 0, -7)
		filtered := allSessions[:0]
		for _, s := range allSessions {
			if s.Surface == surface.KindNotion && !s.LastActive.IsZero() && s.LastActive.Before(cutoff) {
				continue
			}
			filtered = append(filtered, s)
		}
		allSessions = filtered
	}

	sort.SliceStable(allSessions, func(i, j int) bool {
		if allSessions[i].LastActive.IsZero() {
			return false
		}
		if allSessions[j].LastActive.IsZero() {
			return true
		}
		return allSessions[i].LastActive.After(allSessions[j].LastActive)
	})
	families := 15
	if showAll || catalogBacked {
		families = -1
	}
	if catalogBacked {
		projectID, err := a.selectedProject(ctx, views, projectSelector, rootCwd)
		if err != nil {
			return err
		}
		allSessions = filterListScope(allSessions, views, scope, projectID)
	}
	rows := listFamilies(allSessions, families)
	rollups := listRollups(allSessions)
	allSessions = allSessions[:0]
	for _, row := range rows {
		session := row.session
		if rollup, found := rollups[session.ID]; found {
			session.Subagents = &rollup
		}
		allSessions = append(allSessions, session)
	}
	if jsonOut {
		rowsOut := make([]listSessionView, 0, len(allSessions))
		for _, session := range allSessions {
			view := listSessionView{Session: session, Host: localHost, listCatalogView: views[session.ID]}
			if row, remote := remoteRows[session.ID]; remote {
				host := row.Host
				view.Host, view.UnavailableReason = &host, row.Unavailable
			}
			rowsOut = append(rowsOut, view)
		}
		document := map[string]any{"sessions": rowsOut, "errors": surfaceErrors}
		if catalogBacked {
			document["catalog"] = listCatalogMetadata(catalog)
			document["scope"] = scope
		}
		if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
			return err
		}
		if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}
	for name, message := range surfaceErrors {
		if catalogBacked {
			fmt.Fprintf(os.Stderr, "warning: %s catalog health: %s\n", name, message)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s discovery failed: %s\n", name, message)
		}
	}
	if len(allSessions) == 0 {
		fmt.Println("no sessions found")
		if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
			return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
		}
		return nil
	}

	wide := hasFlag(args, "--wide")
	projectHeading := "PROJECT"
	projectWidth := 20
	if wide || !catalogBacked {
		projectHeading = "CWD"
		projectWidth = 48
	}
	showHost := pairedHosts > 0
	if showHost {
		fmt.Printf("%-12s ", "HOST")
	}
	var configRoots []surface.ClaudeConfigRoot
	rootWidth := 0
	if wide {
		rootWidth = 20
		if home, err := os.UserHomeDir(); err == nil {
			configRoots, _ = surface.DescribeClaudeConfigRoots(home)
		}
	}
	fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n", "SURFACE", "STAT", "AGENT", "SESSION", projectWidth, projectHeading, listRootCell(wide, rootWidth, "CONFIG")+"LAST")
	if showHost {
		fmt.Printf("%-12s ", "------------")
	}
	fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n", "-------", "-----", "--------------", "----------------------------", projectWidth, strings.Repeat("-", len(projectHeading)), listRootCell(wide, rootWidth, strings.Repeat("-", rootWidth))+"----------")
	queueCounts := map[string]int{}
	if a.registry() != nil {
		var err error
		queueCounts, err = a.registry().QueueCounts()
		if err != nil {
			return fmt.Errorf("read queue counts: %w", err)
		}
	}
	for index, s := range allSessions {
		row, remote := remoteRows[s.ID]
		queued := queueCounts[s.ID]
		if remote {
			queued = row.QueueCount
		}
		stat := sessStat(s, queued)
		if remote && row.Unavailable != "" {
			stat = "down"
		}
		if showHost {
			hostName := localHost.Name
			if remote {
				hostName = row.Host.Name
			}
			fmt.Printf("%-12s ", truncate(hostName, 12))
		}
		agent := ""
		if alias, ok := aliased[s.ID]; ok {
			agent = "@" + alias
		}
		project := listProjectColumn(views[s.ID])
		if wide || !catalogBacked {
			project = s.Cwd
		}
		last := relTime(s.LastActive)
		name := listRowName(rows[index])
		if rows[index].depth > 0 {
			name = strings.Repeat("  ", rows[index].depth-1) + "└ " + name
		}
		if label := workingSubagentsLabel(s.Subagents); label != "" {
			last += "  " + label
		}
		if len(s.Holders) > 1 {
			last += "  " + listHolders(s.Holders)
		}
		configRoot := ""
		if wide && s.Surface == surface.KindClaude {
			if view := views[s.ID].ClaudeConfigRoot; view != nil {
				configRoot = view.Name
			} else if !remote {
				if derived := surface.ClaudeSessionConfigRoot(configRoots, s.Transcript); derived != nil {
					configRoot = derived.Name
				}
			}
		}
		fmt.Printf("%-7s %-5s %-14s %-28s %-*s %s\n",
			s.Surface, stat, truncate(agent, 14), truncate(name, 28), projectWidth, project, listRootCell(wide, rootWidth, truncate(configRoot, rootWidth))+last)
	}
	if successfulSurfaces == 0 && len(surfaceErrors) > 0 {
		return fmt.Errorf("%d surface(s) failed discovery", len(surfaceErrors))
	}
	return nil
}

func listHolders(holders []surface.Holder) string {
	parts := make([]string, 0, len(holders))
	for _, holder := range holders {
		part := strconv.Itoa(holder.PID)
		if holder.TTY != "" {
			part += " " + holder.TTY
		}
		parts = append(parts, part)
	}
	return fmt.Sprintf("%d terminals: %s", len(holders), strings.Join(parts, ", "))
}

func listRootCell(wide bool, width int, value string) string {
	if !wide {
		return ""
	}
	return fmt.Sprintf("%-*s ", width, value)
}

type listCatalogSurface struct {
	Surface    string    `json:"surface"`
	Health     string    `json:"health"`
	Detail     string    `json:"detail,omitempty"`
	ObservedAt time.Time `json:"observedAt"`
}

type listCatalogMetadataView struct {
	Source     string                          `json:"source"`
	HostEpoch  string                          `json:"hostEpoch"`
	CatalogSeq uint64                          `json:"catalogSeq"`
	Surfaces   []listCatalogSurface            `json:"surfaces"`
	Freshness  map[string]listCatalogFreshness `json:"freshness"`
}

type listCatalogFreshness struct {
	Generation        uint64    `json:"generation"`
	ObservedAt        time.Time `json:"observedAt"`
	Stale             bool      `json:"stale"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
}

func (a *App) listCatalogSnapshot() (registry.CatalogSnapshot, bool, error) {
	if a.registry() == nil || !a.daemonIsRunning() {
		return registry.CatalogSnapshot{}, false, nil
	}
	snapshot, err := a.registry().CatalogSnapshot()
	if err != nil {
		return registry.CatalogSnapshot{}, true, fmt.Errorf("read daemon catalog: %w", err)
	}
	return snapshot, true, nil
}

func (a *App) daemonIsRunning() bool {
	if a.catalogDaemonRunning != nil {
		return a.catalogDaemonRunning()
	}
	_, running := daemon.IsRunning()
	return running
}

func listCatalogMetadata(snapshot registry.CatalogSnapshot) listCatalogMetadataView {
	metadata := listCatalogMetadataView{
		Source:     "daemon",
		HostEpoch:  snapshot.HostEpoch,
		CatalogSeq: snapshot.CatalogSeq,
		Surfaces:   make([]listCatalogSurface, 0, len(snapshot.Surfaces)),
		Freshness:  make(map[string]listCatalogFreshness, len(snapshot.Sessions)),
	}
	for _, record := range snapshot.Surfaces {
		metadata.Surfaces = append(metadata.Surfaces, listCatalogSurface{Surface: string(record.Surface), Health: record.Health, Detail: record.Detail, ObservedAt: record.ObservedAt})
	}
	for _, record := range snapshot.Sessions {
		metadata.Freshness[hosts.Qualify(record.HostID, record.Session.ID)] = listCatalogFreshness{Generation: record.Freshness.Generation, ObservedAt: record.Freshness.ObservedAt, Stale: record.Freshness.Stale, UnavailableReason: record.UnavailableReason}
	}
	return metadata
}

func normalizeSessionCwd(session surface.Session) (surface.Session, error) {
	if session.Cwd == "" {
		return session, nil
	}
	normalized, err := workspace.NormalizeCWD(session.Cwd)
	if err != nil {
		return session, err
	}
	session.Cwd = normalized
	return session, nil
}
