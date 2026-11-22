package daemon

import (
	"slices"

	"github.com/zm2231/agenthail/internal/projectresolve"
)

func (d *Daemon) refreshProjectLinks() error {
	links, err := projectresolve.LoadProjectLinks(d.Registry)
	if err != nil {
		return err
	}
	d.catalogIdentities.SetLinks(links)
	return nil
}

// Each pair is named once, on the project with fewer sessions, which is the
// one a link would usually join into the other.
func suggestSameHistory(byID map[string]*dashboardProject, order []string, roots map[string][]string) {
	for i, left := range order {
		for _, right := range order[i+1:] {
			a, b := byID[left], byID[right]
			if a.LinkHeld != "" || b.LinkHeld != "" || !shareRoot(roots[left], roots[right]) {
				continue
			}
			if a.SessionCount > b.SessionCount {
				a, b = b, a
			}
			a.SameHistory = append(a.SameHistory, projectresolve.CatalogProjectRef{ID: b.ID, DisplayName: b.DisplayName})
		}
	}
}

func shareRoot(a, b []string) bool {
	for _, root := range a {
		if slices.Contains(b, root) {
			return true
		}
	}
	return false
}
