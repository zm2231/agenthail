package daemon

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// A set with no provider is a host that could not answer.
type usageSet struct {
	Host     string       `json:"host"`
	HostID   string       `json:"hostId"`
	Local    bool         `json:"local"`
	Provider string       `json:"provider,omitempty"`
	Name     string       `json:"name,omitempty"`
	Account  string       `json:"account,omitempty"`
	Root     string       `json:"root,omitempty"`
	Primary  bool         `json:"primary,omitempty"`
	Meters   []usageMeter `json:"meters"`
	AsOf     time.Time    `json:"asOf,omitzero"`
	Problem  string       `json:"problem,omitempty"`
}

func (d *Daemon) usageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	var sets, remote []usageSet
	var wait sync.WaitGroup
	wait.Go(func() { sets = d.localUsageSets(r.Context(), time.Now()) })
	if r.URL.Query().Get("scope") != "local" {
		wait.Go(func() { remote = d.remoteUsageSets(r.Context()) })
	}
	wait.Wait()
	sets = append(sets, remote...)
	for i := range sets {
		if sets[i].Meters == nil {
			sets[i].Meters = []usageMeter{}
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sets": sets})
}

func (d *Daemon) localUsageSets(ctx context.Context, now time.Time) []usageSet {
	var claude []usageSet
	var codex *usageSet
	var wait sync.WaitGroup
	wait.Go(func() { claude = d.claudeUsageSets(ctx, now) })
	wait.Go(func() { codex = d.codexUsageSet(ctx, now) })
	wait.Wait()
	sets := []usageSet{}
	if len(claude) > 0 {
		sets = append(sets, claude[0])
	}
	if codex != nil {
		sets = append(sets, *codex)
	}
	if len(claude) > 1 {
		sets = append(sets, claude[1:]...)
	}
	local := d.localHost()
	for i := range sets {
		sets[i].Host, sets[i].HostID, sets[i].Local = local.Name, local.ID, true
	}
	return sets
}

func (d *Daemon) claudeUsageSets(ctx context.Context, now time.Time) []usageSet {
	reader, ok := d.adapters().ForSurface(surface.KindClaude).(claudeConfigRootReader)
	if !ok {
		return nil
	}
	roots := d.claudeConfigRoots()
	sets := make([]usageSet, len(roots))
	var wait sync.WaitGroup
	for i, root := range roots {
		wait.Go(func() { sets[i] = d.claudeUsageSet(ctx, reader, root, now) })
	}
	wait.Wait()
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].Primary && !sets[j].Primary })
	return sets
}

const claudeNoLoginProblem = "This config root has no claude.ai login (it may use an API key), so claude.ai reports no usage for it."

func (d *Daemon) claudeUsageSet(ctx context.Context, reader claudeConfigRootReader, root surface.ClaudeConfigRoot, now time.Time) usageSet {
	set := usageSet{Provider: "claude", Name: root.Name, Account: claudeAccount(root), Root: root.Root, Primary: root.Primary}
	switch {
	case root.Error != "":
		set.Problem = root.Error
		return set
	case root.SignedInAs == nil || root.SignedInAs.AccountID == "":
		set.Problem = claudeNoLoginProblem
		return set
	}
	live, usage, err := d.claudeUsage.read(ctx, reader, root, now)
	if usage != nil {
		set.AsOf = usage.FetchedAt
	}
	set.Meters = usageMeters("claude", usage, now)
	switch {
	case err != nil:
		set.Problem = err.Error()
	case live.UsageProblem != "":
		set.Problem = live.UsageProblem
	case live.SigninProblem != "":
		set.Problem = "no browser sign-in profile here is signed into this login on claude.ai; agenthail signin list shows which accounts they hold"
	}
	return set
}

func claudeAccount(root surface.ClaudeConfigRoot) string {
	if root.SignedInAs != nil && root.SignedInAs.Email != "" {
		return root.SignedInAs.Email
	}
	return root.Name
}

func (d *Daemon) codexUsageSet(ctx context.Context, now time.Time) *usageSet {
	reader, ok := d.adapters().ForSurface(surface.KindCodex).(codexUsageReader)
	if !ok || !d.surfaceHealthy(surface.KindCodex) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, codexUsageTimeout)
	defer cancel()
	usage, err := reader.Usage(ctx)
	switch {
	case errors.Is(err, surfaces.ErrNoCodexUsage):
		return nil
	case err != nil:
		return &usageSet{Provider: "codex", Problem: err.Error()}
	}
	return &usageSet{Provider: "codex", Meters: usageMeters("codex", usage, now)}
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
