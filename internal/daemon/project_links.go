package daemon

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

// A link this host holds but cannot apply is held with its reason, and its
// joined project stays a project of its own.
type projectLinkSet struct {
	survivors map[string]string
	names     map[string]string
	held      map[string]string
	moved     map[string]string
}

func loadProjectLinks(reg *registry.Registry) (projectLinkSet, error) {
	set := projectLinkSet{survivors: map[string]string{}, names: map[string]string{}, held: map[string]string{}, moved: map[string]string{}}
	if reg == nil {
		return set, nil
	}
	moved, err := reg.ProjectMoveTargets()
	if err != nil {
		return set, err
	}
	set.moved = moved
	links, err := reg.ProjectLinks()
	if err != nil {
		return set, err
	}
	records, err := reg.ListProjects()
	if err != nil {
		return set, err
	}
	active := map[string]string{}
	for _, link := range links {
		if link.Linked {
			active[link.Joined] = link.Survivor
		}
	}
	for joined, survivor := range active {
		if _, chained := active[survivor]; chained {
			set.held[joined] = fmt.Sprintf("%s is itself linked into another project", projectIDName(survivor, records))
			continue
		}
		held, err := reg.ProjectHistory(joined)
		if err != nil {
			return set, err
		}
		if len(held) > 0 {
			set.held[joined] = fmt.Sprintf("%s already has %s of its own", projectIDName(joined, records), strings.Join(held, " and "))
			continue
		}
		set.survivors[joined] = survivor
		set.names[survivor] = projectIDName(survivor, records)
	}
	return set, nil
}

func (d *Daemon) refreshProjectLinks() error {
	links, err := loadProjectLinks(d.Registry)
	if err != nil {
		return err
	}
	d.catalogIdentities.setLinks(links)
	return nil
}

// A survivor may have no checkout on this host, so its name comes from its id.
func projectIDName(id string, records []registry.ProjectRecord) string {
	for _, record := range records {
		if record.ID == id && record.Title != "" {
			return record.Title
		}
	}
	if repository, found := strings.CutPrefix(id, hosts.RepositoryPrefix); found {
		repository, _, _ = strings.Cut(repository, "::")
		return path.Base(repository)
	}
	if folder, found := strings.CutPrefix(id, "implicit:"); found {
		return catalogProjectName(filepath.Clean(folder))
	}
	return id
}

func (s projectLinkSet) follow(found *CatalogProject) *CatalogProject {
	if found == nil {
		return nil
	}
	to, moved := s.moved[found.Key]
	if !moved {
		return found
	}
	followed := *found
	if followed.ID == found.Key {
		followed.ID = to
	}
	followed.Key = to
	return &followed
}

func (s projectLinkSet) survivorOf(found *CatalogProject) (string, bool) {
	if found == nil {
		return "", false
	}
	survivor, linked := s.survivors[found.ID]
	return survivor, linked
}

func (s projectLinkSet) idOf(id string) string {
	if survivor, linked := s.survivors[id]; linked {
		return survivor
	}
	return id
}

// A paired host's row arrives already resolved. A link between shared
// projects reaches every host, so this host's own record of it decides, and
// a host that has not yet taken an unlink cannot keep the projects joined. A
// link naming the other host's own folder stays as that host resolved it.
func (s projectLinkSet) apply(found *CatalogProject) *CatalogProject {
	if found != nil && found.LinkedFrom != nil {
		if !registry.SharedProjectID(found.LinkedFrom.ID) {
			return found
		}
		original := *found
		original.ID, original.Key, original.DisplayName, original.LinkedFrom = found.LinkedFrom.ID, found.LinkedFrom.ID, found.LinkedFrom.DisplayName, nil
		found = &original
	}
	found = s.follow(found)
	survivor, linked := s.survivorOf(found)
	if !linked {
		return found
	}
	aliased := *found
	aliased.LinkedFrom = &CatalogProjectRef{ID: found.ID, DisplayName: found.DisplayName}
	aliased.ID, aliased.Key = survivor, ""
	if registry.SharedProjectID(survivor) {
		aliased.Key = survivor
	}
	aliased.DisplayName = s.names[survivor]
	return &aliased
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
			a.SameHistory = append(a.SameHistory, CatalogProjectRef{ID: b.ID, DisplayName: b.DisplayName})
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

func (s projectLinkSet) equal(other projectLinkSet) bool {
	return sameStrings(s.survivors, other.survivors) && sameStrings(s.names, other.names) && sameStrings(s.moved, other.moved)
}

func sameStrings(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, found := b[key]; !found || other != value {
			return false
		}
	}
	return true
}
