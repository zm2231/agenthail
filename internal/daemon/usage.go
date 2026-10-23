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
	Account  string       `json:"account,omitempty"`
	Root     string       `json:"root,omitempty"`
	Primary  bool         `json:"primary,omitempty"`
	Meters   []usageMeter `json:"meters"`
	Problem  string       `json:"problem,omitempty"`
}

func (d *Daemon) usageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	sets := d.localUsageSets(r.Context(), time.Now())
	if r.URL.Query().Get("scope") != "local" {
		sets = append(sets, d.remoteUsageSets(r.Context())...)
	}
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
		wait.Go(func() { sets[i] = claudeUsageSet(ctx, reader, root, now) })
	}
	wait.Wait()
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].Primary && !sets[j].Primary })
	return sets
}

func claudeUsageSet(ctx context.Context, reader claudeConfigRootReader, root surface.ClaudeConfigRoot, now time.Time) usageSet {
	set := usageSet{Provider: "claude", Account: claudeAccount(root), Root: root.Root, Primary: root.Primary}
	ctx, cancel := context.WithTimeout(ctx, claudeRootUsageTimeout)
	defer cancel()
	live, err := reader.ClaudeConfigRoot(ctx, root.Root)
	if err != nil {
		set.Meters, set.Problem = usageMeters("claude", root.Usage, now), err.Error()
		return set
	}
	if live.SignedInAs != nil && live.SignedInAs.Email != "" {
		set.Account = live.SignedInAs.Email
	}
	usage := live.Usage
	if usage == nil {
		usage = root.Usage
	}
	set.Meters, set.Problem = usageMeters("claude", usage, now), live.UsageProblem
	if set.Problem == "" && live.SigninProblem != "" {
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
