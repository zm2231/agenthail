package registry

import "time"

const (
	PRSourceAgent   = "agent"
	PRSourceCreated = "created"
	PRSourceBranch  = "branch"
	PRSourcePerson  = "person"
)

type SessionPR struct {
	SessionID  string
	Repo       string
	Number     int
	URL        string
	Source     string
	Watch      bool
	Title      string
	State      string
	HeadSHA    string
	Mergeable  string
	Review     string
	Checks     string
	LastReadAt time.Time
	LinkedAt   time.Time
}

// LinkPR records a pull request for a session. An explicit link restores one
// that was unlinked; a found one (created or branch) never does, so the
// person's unlink holds. False when the session already had it linked.
func (r *Registry) LinkPR(link SessionPR, explicit bool, at time.Time) (bool, error) {
	result, err := r.db.Exec(`INSERT INTO session_prs(session_id,repo,number,url,source,title,state,head_sha,linked_at_ms) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(session_id,repo,number) DO UPDATE SET unlinked=0,watch=1,source=excluded.source,url=excluded.url,linked_at_ms=excluded.linked_at_ms,
	title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE title END,
	state=CASE WHEN excluded.state<>'' THEN excluded.state ELSE state END,
	head_sha=CASE WHEN excluded.head_sha<>'' THEN excluded.head_sha ELSE head_sha END
WHERE unlinked=1 AND ?`,
		link.SessionID, link.Repo, link.Number, link.URL, link.Source, link.Title, link.State, link.HeadSHA, at.UnixMilli(), explicit)
	if err != nil {
		return false, err
	}
	linked, err := result.RowsAffected()
	return linked == 1, err
}

// UnlinkPR hides a session's pull request and stops watching it; the row
// stays so finding the same pull request again does not bring it back.
func (r *Registry) UnlinkPR(sessionID, repo string, number int) (bool, error) {
	result, err := r.db.Exec(`UPDATE session_prs SET unlinked=1,watch=0 WHERE session_id=? AND repo=? AND number=? AND unlinked=0`, sessionID, repo, number)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (r *Registry) SetPRWatch(sessionID, repo string, number int, watch bool) (bool, error) {
	result, err := r.db.Exec(`UPDATE session_prs SET watch=? WHERE session_id=? AND repo=? AND number=? AND unlinked=0`, watch, sessionID, repo, number)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (r *Registry) SessionPR(sessionID, repo string, number int) (SessionPR, bool, error) {
	prs, err := r.sessionPRs(`WHERE session_id=? AND repo=? AND number=? AND unlinked=0`, sessionID, repo, number)
	if err != nil || len(prs) == 0 {
		return SessionPR{}, false, err
	}
	return prs[0], true, nil
}

func (r *Registry) PRsForSession(sessionID string) ([]SessionPR, error) {
	return r.sessionPRs(`WHERE session_id=? AND unlinked=0`, sessionID)
}

// SessionPRs is every linked pull request, by session.
func (r *Registry) SessionPRs() (map[string][]SessionPR, error) {
	prs, err := r.sessionPRs(`WHERE unlinked=0`)
	if err != nil {
		return nil, err
	}
	out := map[string][]SessionPR{}
	for _, pr := range prs {
		out[pr.SessionID] = append(out[pr.SessionID], pr)
	}
	return out, nil
}

func (r *Registry) sessionPRs(where string, args ...any) ([]SessionPR, error) {
	rows, err := r.read.Query(`SELECT session_id,repo,number,url,source,watch,title,state,head_sha,mergeable,review,checks,last_read_at_ms,linked_at_ms FROM session_prs `+where+` ORDER BY linked_at_ms,repo,number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionPR
	for rows.Next() {
		var pr SessionPR
		var read, linked int64
		if err := rows.Scan(&pr.SessionID, &pr.Repo, &pr.Number, &pr.URL, &pr.Source, &pr.Watch, &pr.Title, &pr.State, &pr.HeadSHA, &pr.Mergeable, &pr.Review, &pr.Checks, &read, &linked); err != nil {
			return nil, err
		}
		if read > 0 {
			pr.LastReadAt = time.UnixMilli(read).UTC()
		}
		pr.LinkedAt = time.UnixMilli(linked).UTC()
		out = append(out, pr)
	}
	return out, rows.Err()
}
