package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func (d *Daemon) usageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	var sets, remote []usage.Set
	var wait sync.WaitGroup
	wait.Go(func() { sets = d.localUsageSets(r.Context(), time.Now()) })
	if _, fromHost := requestingHost(r.Context()); !fromHost && r.URL.Query().Get("scope") != "local" {
		wait.Go(func() { remote = d.remoteUsageSets(r.Context()) })
	}
	wait.Wait()
	sets = usage.MergeProxySets(append(sets, remote...))
	for i := range sets {
		if sets[i].Meters == nil {
			sets[i].Meters = []usage.Meter{}
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sets": sets})
}

func (d *Daemon) localUsageSets(ctx context.Context, now time.Time) []usage.Set {
	var claude, proxies []usage.Set
	var codex *usage.Set
	var wait sync.WaitGroup
	wait.Go(func() { claude = d.claudeUsageSets(ctx, now) })
	wait.Go(func() { codex = d.codexUsageSet(ctx, now) })
	wait.Go(func() { proxies = d.proxyUsageSets(now) })
	wait.Wait()
	sets := []usage.Set{}
	if len(claude) > 0 {
		sets = append(sets, claude[0])
	}
	if codex != nil {
		sets = append(sets, *codex)
	}
	if len(claude) > 1 {
		sets = append(sets, claude[1:]...)
	}
	sets = append(sets, proxies...)
	local := d.localHost()
	for i := range sets {
		sets[i].Host, sets[i].HostID, sets[i].Local = local.Name, local.ID, true
	}
	return sets
}

func (d *Daemon) claudeUsageSets(ctx context.Context, now time.Time) []usage.Set {
	reader, ok := d.adapters().ForSurface(surface.KindClaude).(claudeConfigRootReader)
	if !ok {
		return nil
	}
	roots := d.currentClaudeConfigRoots()
	sets := make([]usage.Set, len(roots))
	var wait sync.WaitGroup
	for i, root := range roots {
		wait.Go(func() { sets[i] = d.claudeUsageSet(ctx, reader, root, now) })
	}
	wait.Wait()
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].Primary && !sets[j].Primary })
	return sets
}

func (d *Daemon) claudeUsageSet(ctx context.Context, reader claudeConfigRootReader, root surface.ClaudeConfigRoot, now time.Time) usage.Set {
	set := usage.Set{Provider: "claude", Name: root.Name, Root: root.Root, Primary: root.Primary}
	if root.SignedInAs != nil {
		set.Account = root.SignedInAs.Email
	}
	if root.Error != "" {
		set.Problem = root.Error
		return set
	}
	if home, err := os.UserHomeDir(); err == nil {
		set.Login, set.LoginSource = surface.ClaudeRootLogin(home, root)
	}
	if set.Login == surface.ClaudeLoginProxy {
		set.Proxy = d.rootProxy(root.Proxy)
		return set
	}
	if problem, detail, ok := usage.ClaudeNoUsageProblem(set.Login, set.LoginSource); ok {
		set.Problem, set.Detail = problem, detail
		return set
	}
	live, got, failures, err := d.claudeUsage.Read(ctx, reader, root, now)
	if got != nil {
		set.AsOf = got.FetchedAt
	}
	set.Meters = usage.Meters("claude", got, now)
	switch {
	case got != nil && failures < usage.FailuresHeld:
	case err != nil:
		set.Problem = err.Error()
	case live.UsageProblem != "":
		set.Problem = live.UsageProblem
	case live.SigninProblem != "":
		set.Problem = "no browser sign-in profile here is signed into this login on claude.ai; agenthail signin list shows which accounts they hold"
	}
	return set
}

func (d *Daemon) codexUsageSet(ctx context.Context, now time.Time) *usage.Set {
	reader, ok := d.adapters().ForSurface(surface.KindCodex).(codexUsageReader)
	if !ok || !d.surfaceHealthy(surface.KindCodex) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, codexUsageTimeout)
	defer cancel()
	read, err := reader.Usage(ctx)
	switch {
	case errors.Is(err, codexsurface.ErrNoCodexUsage):
		return nil
	case err != nil:
		return &usage.Set{Provider: "codex", Problem: err.Error()}
	}
	return &usage.Set{Provider: "codex", Meters: usage.Meters("codex", read, now)}
}

func (d *Daemon) surfaceHealthy(kind surface.SurfaceKind) bool {
	if d.Registry == nil {
		return false
	}
	states, err := d.Registry.CatalogSurfaces()
	if err != nil {
		return false
	}
	for _, state := range states {
		if state.Surface == kind {
			return state.Health == "healthy"
		}
	}
	return false
}
