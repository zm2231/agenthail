package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

// ProjectLink records that the project Joined resolves as Survivor. An
// unlinked record stays as a tombstone, so a host that still holds the link
// takes the unlink rather than reviving it. Of two records for one project
// the later wins, the origin host breaking a tie.
type ProjectLink struct {
	Joined     string    `json:"joined"`
	Survivor   string    `json:"survivor"`
	Linked     bool      `json:"linked"`
	ChangedAt  time.Time `json:"changedAt"`
	OriginHost string    `json:"originHost"`
}

// A link with a folder of this host's own on either side stays on this host.
func (l ProjectLink) Shared() bool {
	return SharedProjectID(l.Joined) && SharedProjectID(l.Survivor)
}

func SharedProjectID(id string) bool {
	return len(id) > len(hosts.RepositoryPrefix) && id[:len(hosts.RepositoryPrefix)] == hosts.RepositoryPrefix
}

func (l ProjectLink) newerThan(other ProjectLink) bool {
	if !l.ChangedAt.Equal(other.ChangedAt) {
		return l.ChangedAt.After(other.ChangedAt)
	}
	return l.OriginHost > other.OriginHost
}

func (r *Registry) ProjectLinks() ([]ProjectLink, error) {
	rows, err := r.read.Query(`SELECT joined,survivor,linked,changed_at_ms,origin_host FROM project_links ORDER BY joined`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []ProjectLink{}
	for rows.Next() {
		var link ProjectLink
		var at int64
		if err := rows.Scan(&link.Joined, &link.Survivor, &link.Linked, &at, &link.OriginHost); err != nil {
			return nil, err
		}
		link.ChangedAt = time.UnixMilli(at)
		links = append(links, link)
	}
	return links, rows.Err()
}

func (r *Registry) SetProjectLink(joined, survivor string, linked bool) (ProjectLink, error) {
	if joined == "" || (linked && survivor == "") || joined == survivor {
		return ProjectLink{}, fmt.Errorf("a link needs two different projects")
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return ProjectLink{}, fmt.Errorf("read this host's identity: %w", err)
	}
	link := ProjectLink{Joined: joined, Survivor: survivor, Linked: linked, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity.ID}
	tx, err := r.db.Begin()
	if err != nil {
		return ProjectLink{}, err
	}
	defer tx.Rollback()
	if current, found, err := projectLinkTx(tx, joined); err != nil {
		return ProjectLink{}, err
	} else if found && !link.ChangedAt.After(current.ChangedAt) {
		link.ChangedAt = current.ChangedAt.Add(time.Millisecond)
	}
	if err := putProjectLinkTx(tx, link); err != nil {
		return ProjectLink{}, err
	}
	if linked {
		if err := moveLeadHistoryTx(tx, joined, survivor); err != nil {
			return ProjectLink{}, err
		}
	}
	return link, tx.Commit()
}

func (r *Registry) AdoptProjectLinks(links []ProjectLink) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, link := range links {
		if !link.Shared() || link.OriginHost == "" {
			continue
		}
		current, found, err := projectLinkTx(tx, link.Joined)
		if err != nil {
			return false, err
		}
		if found && !link.newerThan(current) {
			continue
		}
		if err := putProjectLinkTx(tx, link); err != nil {
			return false, err
		}
		if link.Linked {
			if err := moveLeadHistoryTx(tx, link.Joined, link.Survivor); err != nil {
				return false, err
			}
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, tx.Commit()
}

func projectLinkTx(tx *sql.Tx, joined string) (ProjectLink, bool, error) {
	link := ProjectLink{Joined: joined}
	var at int64
	err := tx.QueryRow(`SELECT survivor,linked,changed_at_ms,origin_host FROM project_links WHERE joined=?`, joined).Scan(&link.Survivor, &link.Linked, &at, &link.OriginHost)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectLink{}, false, nil
	}
	link.ChangedAt = time.UnixMilli(at)
	return link, err == nil, err
}

func putProjectLinkTx(tx *sql.Tx, link ProjectLink) error {
	if _, err := tx.Exec(`INSERT INTO project_links(joined,survivor,linked,changed_at_ms,origin_host) VALUES(?,?,?,?,?)
		ON CONFLICT(joined) DO UPDATE SET survivor=excluded.survivor, linked=excluded.linked, changed_at_ms=excluded.changed_at_ms, origin_host=excluded.origin_host`,
		link.Joined, link.Survivor, link.Linked, link.ChangedAt.UnixMilli(), link.OriginHost); err != nil {
		return err
	}
	payload, err := json.Marshal(link)
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("project.linked:%s:%d:%s", link.Joined, link.ChangedAt.UnixMilli(), link.OriginHost), Type: "project.linked", EntityID: link.Joined, Payload: payload})
	return err
}

// A joined project's past leads become its survivor's, except while it still
// has a lead on this host, whose own record CoordinatorSince reads.
func moveLeadHistoryTx(tx *sql.Tx, joined, survivor string) error {
	_, err := tx.Exec(`UPDATE coordinator_history SET project_id=?2 WHERE project_id=?1
		AND NOT EXISTS (SELECT 1 FROM coordinators WHERE project_id=?1 AND host_id='')`, joined, survivor)
	return err
}

// ProjectHistory names what a project holds under its own id that a link
// would strand: at most one lead entry and one ledger entry.
func (r *Registry) ProjectHistory(id string) ([]string, error) {
	type check struct{ what, query string }
	var held []string
	for _, group := range [][]check{
		{
			{"a lead", `SELECT COUNT(*) FROM coordinators WHERE project_id=?`},
		},
		{
			{"a ledger board", `SELECT COUNT(*) FROM ledger_events WHERE project_key=?`},
			{"ledger grants", `SELECT COUNT(*) FROM ledger_grants WHERE project_key=?`},
		},
	} {
		for _, c := range group {
			var n int
			if err := r.read.QueryRow(c.query, id).Scan(&n); err != nil {
				return nil, err
			}
			if n > 0 {
				held = append(held, c.what)
				break
			}
		}
	}
	return held, nil
}
