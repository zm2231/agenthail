package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// PRReadFailureLimit is how many reads running GitHub may leave a watched pull
// request out of before its row shows the problem.
const PRReadFailureLimit = 3

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

	FailedChecks   []string
	PassedHead     string
	ConflictHead   string
	RemarksThrough time.Time
	RemarkIDs      []string
	Wakes          int
	CommentWakes   int
	ReadFailures   int
}

func (r *Registry) WatchedPRs() ([]SessionPR, error) {
	return r.sessionPRs(`WHERE watch=1 AND unlinked=0`)
}

// RecordPRRead is false when the row changed since the read saw readWakes:
// re-watch and re-link bump wakes too. That read must not wake anyone.
func (r *Registry) RecordPRRead(pr SessionPR, readWakes int, at time.Time) (bool, error) {
	failed, err := jsonList(pr.FailedChecks)
	if err != nil {
		return false, err
	}
	remarks, err := jsonList(pr.RemarkIDs)
	if err != nil {
		return false, err
	}
	result, err := r.db.Exec(`UPDATE session_prs SET title=?,state=?,head_sha=?,mergeable=?,review=?,checks=?,failed_checks=?,passed_head=?,conflict_head=?,remarks_through_ms=?,remark_ids=?,wakes=?,comment_wakes=?,read_failures=0,last_read_at_ms=?,watch=?
WHERE session_id=? AND repo=? AND number=? AND unlinked=0 AND watch=1 AND wakes=?`,
		pr.Title, pr.State, pr.HeadSHA, pr.Mergeable, pr.Review, pr.Checks, failed, pr.PassedHead, pr.ConflictHead, unixMilli(pr.RemarksThrough), remarks, pr.Wakes, pr.CommentWakes, at.UnixMilli(), pr.Watch,
		pr.SessionID, pr.Repo, pr.Number, readWakes)
	if err != nil {
		return false, err
	}
	recorded, err := result.RowsAffected()
	return recorded == 1, err
}

func (r *Registry) RecordPRReadFailure(sessionID, repo string, number, readWakes int) (bool, error) {
	result, err := r.db.Exec(`UPDATE session_prs SET read_failures=read_failures+1 WHERE session_id=? AND repo=? AND number=? AND unlinked=0 AND watch=1 AND wakes=?`, sessionID, repo, number, readWakes)
	if err != nil {
		return false, err
	}
	recorded, err := result.RowsAffected()
	return recorded == 1, err
}

func jsonList(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(values)
	return string(encoded), err
}

// LinkPR records a pull request for a session. An explicit link restores one
// that was unlinked; a found one (created or branch) never does, so the
// person's unlink holds. False when the session already had it linked.
func (r *Registry) LinkPR(link SessionPR, explicit bool, at time.Time) (bool, error) {
	result, err := r.db.Exec(`INSERT INTO session_prs(session_id,repo,number,url,source,title,state,head_sha,linked_at_ms) VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(session_id,repo,number) DO UPDATE SET unlinked=0,watch=1,wakes=wakes+1,comment_wakes=0,source=excluded.source,url=excluded.url,linked_at_ms=excluded.linked_at_ms,
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
	result, err := r.db.Exec(`UPDATE session_prs SET watch=?1,wakes=wakes+(?1 AND NOT watch),comment_wakes=CASE WHEN ?1 AND NOT watch THEN 0 ELSE comment_wakes END
WHERE session_id=?2 AND repo=?3 AND number=?4 AND unlinked=0`, watch, sessionID, repo, number)
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

// SessionHasPRIn counts unlinked pull requests too, so a branch whose pull
// request the person unlinked is not searched again.
func (r *Registry) SessionHasPRIn(sessionID, repo string) (bool, error) {
	var count int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM session_prs WHERE session_id=? AND repo=?`, sessionID, repo).Scan(&count)
	return count > 0, err
}

// presentationPRColumns are the session_prs columns a session row shows or
// orders by; changing one moves the presentation version.
const presentationPRColumns = "session_id,repo,number,url,source,unlinked,watch,title,state,head_sha,mergeable,review,checks,read_failures,linked_at_ms"

func presentationPRs(ctx context.Context, tx *sql.Tx) (map[string][]SessionPR, error) {
	rows, err := tx.QueryContext(ctx, `SELECT session_id,repo,number,url,source,watch,title,state,head_sha,mergeable,review,checks,CASE WHEN read_failures>=? THEN read_failures ELSE 0 END FROM session_prs WHERE unlinked=0 ORDER BY linked_at_ms,repo,number`, PRReadFailureLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SessionPR{}
	for rows.Next() {
		var pr SessionPR
		if err := rows.Scan(&pr.SessionID, &pr.Repo, &pr.Number, &pr.URL, &pr.Source, &pr.Watch, &pr.Title, &pr.State, &pr.HeadSHA, &pr.Mergeable, &pr.Review, &pr.Checks, &pr.ReadFailures); err != nil {
			return nil, err
		}
		out[pr.SessionID] = append(out[pr.SessionID], pr)
	}
	return out, rows.Err()
}

func (r *Registry) sessionPRs(where string, args ...any) ([]SessionPR, error) {
	rows, err := r.read.Query(`SELECT session_id,repo,number,url,source,watch,title,state,head_sha,mergeable,review,checks,last_read_at_ms,linked_at_ms,
	failed_checks,passed_head,conflict_head,remarks_through_ms,remark_ids,wakes,comment_wakes,read_failures FROM session_prs `+where+` ORDER BY linked_at_ms,repo,number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionPR
	for rows.Next() {
		var pr SessionPR
		var read, linked, through int64
		var failed, remarks string
		if err := rows.Scan(&pr.SessionID, &pr.Repo, &pr.Number, &pr.URL, &pr.Source, &pr.Watch, &pr.Title, &pr.State, &pr.HeadSHA, &pr.Mergeable, &pr.Review, &pr.Checks, &read, &linked,
			&failed, &pr.PassedHead, &pr.ConflictHead, &through, &remarks, &pr.Wakes, &pr.CommentWakes, &pr.ReadFailures); err != nil {
			return nil, err
		}
		for raw, list := range map[string]*[]string{failed: &pr.FailedChecks, remarks: &pr.RemarkIDs} {
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), list); err != nil {
					return nil, err
				}
			}
		}
		if through > 0 {
			pr.RemarksThrough = time.UnixMilli(through).UTC()
		}
		if read > 0 {
			pr.LastReadAt = time.UnixMilli(read).UTC()
		}
		pr.LinkedAt = time.UnixMilli(linked).UTC()
		out = append(out, pr)
	}
	return out, rows.Err()
}
