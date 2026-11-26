package registry

import "time"

// ClaimReviewPush reports whether this call claimed the push of one review of an item, keyed by when the item went into review, so a review pushes once however many sweeps see it.
func (r *Registry) ClaimReviewPush(projectKey, itemID string, inReviewAt, at time.Time) (bool, error) {
	result, err := r.db.Exec(`INSERT OR IGNORE INTO work_review_pushes(project_key, item_id, in_review_at_ms, claimed_at_ms) VALUES (?,?,?,?)`, projectKey, itemID, inReviewAt.UnixMilli(), at.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *Registry) ReleaseReviewPush(projectKey, itemID string, inReviewAt, claimedAt time.Time) error {
	_, err := r.db.Exec(`DELETE FROM work_review_pushes WHERE project_key=? AND item_id=? AND in_review_at_ms=? AND claimed_at_ms=?`, projectKey, itemID, inReviewAt.UnixMilli(), claimedAt.UnixMilli())
	return err
}
