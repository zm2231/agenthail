package registry

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEventSubscriptionLimit = errors.New("this identity has as many event subscriptions as it may hold")
	ErrRemoteCredentialsMoved = errors.New("the remote credentials changed since this request was verified; reconnect")
)

type EventSubscription struct {
	ID              string
	Generation      string
	IdentityID      string
	Name            string
	Args            string
	URL             string
	Secret          string
	PrevSecret      string
	PrevSecretUntil time.Time
	RefreshBefore   time.Time
	VerifiedAt      time.Time
	NotifiedThrough int64
	Failures        int
	SuspendedAt     time.Time
}

func (s EventSubscription) Secrets(now time.Time) []string {
	if s.PrevSecret != "" && now.Before(s.PrevSecretUntil) {
		return []string{s.Secret, s.PrevSecret}
	}
	return []string{s.Secret}
}

type EventSubscribe struct {
	ID            string
	IdentityID    string
	TokenHash     string
	Epoch         int64
	Name          string
	Args          string
	URL           string
	Secret        string
	RefreshBefore time.Time
	Verified      bool
	Limit         int
	SecretGrace   time.Duration
}

const eventSubscriptionColumns = `s.id,s.generation,s.identity_id,s.name,s.args,s.url,s.secret,s.prev_secret,s.prev_secret_until_ms,s.refresh_before_ms,s.verified_at_ms,s.notified_through,s.failures,s.suspended_at_ms`

func millis(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func scanEventSubscription(row rowScanner, extra ...any) (EventSubscription, error) {
	var sub EventSubscription
	var prevUntil, refresh, verified, suspended int64
	dest := append([]any{&sub.ID, &sub.Generation, &sub.IdentityID, &sub.Name, &sub.Args, &sub.URL, &sub.Secret, &sub.PrevSecret, &prevUntil, &refresh, &verified, &sub.NotifiedThrough, &sub.Failures, &suspended}, extra...)
	if err := row.Scan(dest...); err != nil {
		return EventSubscription{}, err
	}
	sub.PrevSecretUntil, sub.RefreshBefore, sub.VerifiedAt, sub.SuspendedAt = fromMillis(prevUntil), fromMillis(refresh), fromMillis(verified), fromMillis(suspended)
	return sub, nil
}

func (r *Registry) EventEndpointVerified(identityID, url string, cutoff time.Time) (bool, error) {
	var found int
	err := r.read.QueryRow(`SELECT 1 FROM mcp_event_subscriptions WHERE identity_id=? AND url=? AND verified_at_ms>=? LIMIT 1`, identityID, url, cutoff.UnixMilli()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Written only while the credentials it was verified with are still current, so a rotation mid-subscribe wins.
func (r *Registry) SubscribeEvent(s EventSubscribe) (EventSubscription, error) {
	now := r.now()
	tx, err := r.db.Begin()
	if err != nil {
		return EventSubscription{}, err
	}
	defer tx.Rollback()
	var epoch int64
	if err := tx.QueryRow(`SELECT epoch FROM mcp_remote_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return EventSubscription{}, err
	}
	var current int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM identity_remote WHERE identity_id=? AND token_hash=?`, s.IdentityID, s.TokenHash).Scan(&current); err != nil {
		return EventSubscription{}, err
	}
	if current == 0 || epoch != s.Epoch {
		return EventSubscription{}, ErrRemoteCredentialsMoved
	}
	if _, err := tx.Exec(`DELETE FROM mcp_event_subscriptions WHERE identity_id=? AND refresh_before_ms<>0 AND refresh_before_ms<=?`, s.IdentityID, now.UnixMilli()); err != nil {
		return EventSubscription{}, err
	}
	var held int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM mcp_event_subscriptions WHERE identity_id=? AND id<>?`, s.IdentityID, s.ID).Scan(&held); err != nil {
		return EventSubscription{}, err
	}
	if held >= s.Limit {
		return EventSubscription{}, fmt.Errorf("%w (%d)", ErrEventSubscriptionLimit, s.Limit)
	}
	verified := int64(0)
	if s.Verified {
		verified = now.UnixMilli()
	}
	if _, err := tx.Exec(`INSERT INTO mcp_event_subscriptions(id,identity_id,token_hash,name,args,url,secret,refresh_before_ms,verified_at_ms,notified_through,created_at_ms,generation)
		VALUES(?1,?2,?3,?4,?5,?6,?7,?8,?9,(SELECT COALESCE(MAX(id),0) FROM agent_inbox WHERE identity_id=?2),?10,?12)
		ON CONFLICT(id) DO UPDATE SET token_hash=excluded.token_hash,generation=excluded.generation,
			prev_secret=CASE WHEN secret<>excluded.secret THEN secret ELSE prev_secret END,
			prev_secret_until_ms=CASE WHEN secret<>excluded.secret THEN ?11 ELSE prev_secret_until_ms END,
			secret=excluded.secret,refresh_before_ms=excluded.refresh_before_ms,
			verified_at_ms=MAX(verified_at_ms,excluded.verified_at_ms),failures=0,suspended_at_ms=0,deferred_until_ms=0`,
		s.ID, s.IdentityID, s.TokenHash, s.Name, s.Args, s.URL, s.Secret, millis(s.RefreshBefore), verified, now.UnixMilli(), now.Add(s.SecretGrace).UnixMilli(), rand.Text()); err != nil {
		return EventSubscription{}, err
	}
	sub, err := scanEventSubscription(tx.QueryRow(`SELECT `+eventSubscriptionColumns+` FROM mcp_event_subscriptions s WHERE s.id=?`, s.ID))
	if err != nil {
		return EventSubscription{}, err
	}
	return sub, tx.Commit()
}

func (r *Registry) UnsubscribeEvent(identityID, id string) (bool, error) {
	result, err := r.db.Exec(`DELETE FROM mcp_event_subscriptions WHERE identity_id=? AND id=?`, identityID, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// Delivery writes name the generation they started under, so one begun before a refresh cannot undo it.
func (r *Registry) DropEventSubscription(id, generation string) error {
	_, err := r.db.Exec(`DELETE FROM mcp_event_subscriptions WHERE id=? AND generation=?`, id, generation)
	return err
}

type DueEvent struct {
	EventSubscription
	Newest int64
	Unread int
}

// The join on token_hash ends delivery the moment the identity's credentials change.
const (
	liveEventFrom  = ` FROM mcp_event_subscriptions s JOIN identity_remote r ON r.identity_id=s.identity_id AND r.token_hash=s.token_hash`
	liveEventWhere = ` WHERE (s.refresh_before_ms=0 OR s.refresh_before_ms>?1)`
)

func (r *Registry) DueEvents(now time.Time, trialEvery time.Duration) ([]DueEvent, error) {
	return queryRows(r.read, []DueEvent{}, func(row rowScanner) (DueEvent, error) {
		var due DueEvent
		sub, err := scanEventSubscription(row, &due.Newest, &due.Unread)
		due.EventSubscription = sub
		return due, err
	}, `SELECT `+eventSubscriptionColumns+`,u.newest,u.unread`+liveEventFrom+`
		JOIN (SELECT identity_id,MAX(id) newest,COUNT(*) unread FROM agent_inbox WHERE read_at_ms=0 GROUP BY identity_id) u ON u.identity_id=s.identity_id AND u.newest>s.notified_through`+
		liveEventWhere+` AND (s.suspended_at_ms=0 OR s.suspended_at_ms<=?2) AND s.deferred_until_ms<=?1 ORDER BY s.id`, now.UnixMilli(), now.Add(-trialEvery).UnixMilli())
}

func (r *Registry) LiveEventSubscription(id string, now time.Time) (EventSubscription, bool, error) {
	sub, err := scanEventSubscription(r.read.QueryRow(`SELECT `+eventSubscriptionColumns+liveEventFrom+liveEventWhere+` AND s.id=?2`, now.UnixMilli(), id))
	if errors.Is(err, sql.ErrNoRows) {
		return EventSubscription{}, false, nil
	}
	return sub, err == nil, err
}

func (r *Registry) EventDelivered(id, generation string, through int64) error {
	_, err := r.db.Exec(`UPDATE mcp_event_subscriptions SET notified_through=MAX(notified_through,?),failures=0,suspended_at_ms=0,deferred_until_ms=0 WHERE id=? AND generation=?`, through, id, generation)
	return err
}

// EventFailed reports whether this failure suspended a subscription that was delivering.
func (r *Registry) EventFailed(id, generation string, suspendAfter int) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var failures int
	var suspended int64
	err = tx.QueryRow(`SELECT failures,suspended_at_ms FROM mcp_event_subscriptions WHERE id=? AND generation=?`, id, generation).Scan(&failures, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	failures++
	if failures >= suspendAfter {
		suspended = r.now().UnixMilli()
	}
	if _, err := tx.Exec(`UPDATE mcp_event_subscriptions SET failures=?,suspended_at_ms=? WHERE id=?`, failures, suspended, id); err != nil {
		return false, err
	}
	return failures == suspendAfter, tx.Commit()
}

func (r *Registry) DeferEvent(id, generation string, until time.Time) error {
	_, err := r.db.Exec(`UPDATE mcp_event_subscriptions SET deferred_until_ms=? WHERE id=? AND generation=?`, until.UnixMilli(), id, generation)
	return err
}

func (r *Registry) PruneExpiredEventSubscriptions(now time.Time) (int64, error) {
	result, err := r.db.Exec(`DELETE FROM mcp_event_subscriptions WHERE refresh_before_ms<>0 AND refresh_before_ms<=?`, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
