package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	workReviewAction = "work-review"
	workReviewEvent  = "work.review"
	reviewApprove    = "approve"
	reviewReject     = "reject"
)

func (d *Daemon) reviewEntries(rows []dashboardSession) []reviewEntry {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		d.log.Printf("reviews: ledger projects: %s", err)
		return nil
	}
	var (
		entries []reviewEntry
		leads   map[string]string
		prs     map[string]registry.SessionPR
	)
	projects := map[string]string{}
	for _, row := range rows {
		projects[row.ID] = projectIDOf(row)
	}
	for _, key := range keys {
		if owner, err := store.RemoteOwner(key); err != nil || owner != "" {
			continue
		}
		board, err := store.Board(key)
		if err != nil {
			continue
		}
		for _, work := range board.WorkItems() {
			if work.State != ledger.WorkInReview {
				continue
			}
			if leads == nil {
				leads, prs = d.ledgerLeads(rows), d.linkedPRs()
			}
			counts := work.ProofCounts()
			item := decisionItem{Key: "review:" + key + ":" + work.ID, Group: decisionReview, Kind: decisionReview, Reason: reviewReason(work), Since: work.InReviewSince(),
				WorkItem: work.ID, WorkTitle: work.Text, LedgerKey: key, Phase: work.Phase, Proof: &counts, PRs: itemPRs(work, prs)}
			if work.Assignee != "" {
				item.SessionID = d.publicSessionID(work.Assignee)
				item.ProjectID = projects[item.SessionID]
			}
			entries = append(entries, reviewEntry{item: item, lead: leads[key], passed: work.ReviewPass != nil})
		}
	}
	return entries
}

func reviewReason(work ledger.WorkItem) string {
	switch {
	case work.ReviewPass != nil:
		return work.ReviewPass.Reason
	case work.Reason != "":
		return work.Reason
	}
	return "Ready for review"
}

func (d *Daemon) ledgerLeads(rows []dashboardSession) map[string]string {
	directory, err := d.loadLeadDirectory()
	if err != nil {
		d.log.Printf("reviews: read the leads: %s", err)
		return map[string]string{}
	}
	leads, seen := map[string]string{}, map[string]bool{}
	for _, row := range rows {
		if row.Project == nil || seen[row.Project.ID] {
			continue
		}
		seen[row.Project.ID] = true
		lead, ok := directory.leadOf(row.Project)
		if !ok {
			continue
		}
		if key, err := projectresolve.LedgerKey(d.Registry, row.Project); err == nil && key != "" {
			leads[key] = d.publicSessionID(lead.sessionID)
		}
	}
	return leads
}

// The ledger does not know who leads a project, so this host checks a review-pass against its coordinator records before the write is recorded here or sent to the ledger's owner.
func (d *Daemon) refuseReviewPass(path string, body []byte) error {
	if path != "/api/v1/ledger/work" {
		return nil
	}
	var req ledger.WorkRequest
	if json.Unmarshal(body, &req) != nil || req.Action != ledger.WorkReviewPass || d.leadsLedger(req.SessionID, req.ProjectKey) {
		return nil
	}
	return fmt.Errorf("only the lead of the project whose ledger is %s passes a review on to the person; other sessions review with approve or reject", req.ProjectKey)
}

func (d *Daemon) leadsLedger(sessionID, key string) bool {
	session, err := d.Registry.Session(sessionID)
	if err != nil || session == nil {
		return false
	}
	project, err := d.sessionProject(*session)
	if err != nil || project == nil {
		return false
	}
	directory, err := d.loadLeadDirectory()
	if err != nil {
		return false
	}
	if lead, ok := directory.leadOf(project); !ok || lead.sessionID != session.ID {
		return false
	}
	own, err := projectresolve.LedgerKey(d.Registry, project)
	return err == nil && own == key
}

func (d *Daemon) anyReviewHere() bool {
	store := d.ledgerStore()
	keys, err := store.ProjectKeys()
	if err != nil {
		return false
	}
	for _, key := range keys {
		if owner, err := store.RemoteOwner(key); err != nil || owner != "" {
			continue
		}
		if board, err := store.Board(key); err == nil && slices.ContainsFunc(board.WorkItems(), func(item ledger.WorkItem) bool { return item.State == ledger.WorkInReview }) {
			return true
		}
	}
	return false
}

func (d *Daemon) personReviews(ctx context.Context) ([]decisionItem, error) {
	rows, _, err := d.decisionRows(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]dashboardSession, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	var reviews []decisionItem
	for _, entry := range d.reviewEntries(rows) {
		if !entry.leadHolds(byID) {
			reviews = append(reviews, entry.item)
		}
	}
	return reviews, nil
}

// A review pushes once when it reaches the person: when the item goes into review with no lead to take it, or when its lead passes it on or stops being able to act.
func (d *Daemon) sweepReviewPushes(ctx context.Context) {
	if !d.anyReviewHere() {
		return
	}
	reviews, err := d.personReviews(ctx)
	if err != nil {
		d.log.Printf("reviews: read the sessions: %s", err)
		return
	}
	for _, review := range reviews {
		d.pushReview(ctx, review)
	}
}

func (d *Daemon) pushReview(ctx context.Context, review decisionItem) {
	claimedAt := time.Now()
	claimed, err := d.Registry.ClaimReviewPush(review.LedgerKey, review.WorkItem, review.Since, claimedAt)
	if err != nil {
		d.log.Printf("reviews: claim the push of %s: %s", review.Key, err)
		return
	}
	if !claimed {
		return
	}
	if d.reviewPushRead != nil {
		d.reviewPushRead()
	}
	reviews, err := d.personReviews(ctx)
	if err != nil || !slices.ContainsFunc(reviews, func(other decisionItem) bool { return other.Key == review.Key && other.Since.Equal(review.Since) }) {
		if err := d.Registry.ReleaseReviewPush(review.LedgerKey, review.WorkItem, review.Since, claimedAt); err != nil {
			d.log.Printf("reviews: release the push of %s: %s", review.Key, err)
		}
		return
	}
	message := shownLine(review.WorkTitle, headlineShownRunes) + " is ready for your review"
	hostID := d.hostIdentity().ID
	collapse := reviewPushKey(hostID, review.LedgerKey, review.WorkItem, "")
	mobile := mobileNotification{Title: "Review", Message: message, SessionID: review.SessionID, EventType: workReviewEvent, CollapseID: collapse,
		DedupeKey: reviewPushKey(hostID, review.LedgerKey, review.WorkItem, strconv.FormatInt(review.Since.UnixMilli(), 10))}
	d.sendTurnNeedsYou(review.SessionID, message, mobile)
}

func reviewPushKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "review-" + hex.EncodeToString(sum[:16])
}

func (d *Daemon) doWorkReview(_ context.Context, p principal, request dashboardActionRequest) (actionResult, error) {
	if !p.forThePerson() {
		return actionResult{}, actionFailed(http.StatusForbidden, "forbidden", "only the person reviews through work-review; a session approves or sends back with the ledger's approve and reject")
	}
	key, itemID, verdict, note := strings.TrimSpace(request.LedgerKey), strings.TrimSpace(request.WorkItem), request.Verdict, strings.TrimSpace(request.Note)
	switch {
	case key == "" || itemID == "":
		return actionResult{}, invalidRequest("work-review needs ledgerKey and workItem")
	case verdict != reviewApprove && verdict != reviewReject:
		return actionResult{}, invalidRequest("work-review verdict is approve or reject")
	case verdict == reviewReject && note == "":
		return actionResult{}, invalidRequest("sending work back needs a note: what it still needs")
	}
	store := d.ledgerStore()
	if owner, err := store.RemoteOwner(key); err != nil || owner != "" {
		return actionResult{}, staleRequest()
	}
	board, err := store.Board(key)
	if err != nil {
		return actionResult{}, staleRequest()
	}
	work, found := board.WorkItem(itemID)
	if !found || work.State != ledger.WorkInReview || work.Assignee == "" {
		return actionResult{}, staleRequest()
	}
	words, action, outcome := "Approved.", ledger.WorkApprove, "approved"
	if verdict == reviewReject {
		words, action, outcome = "Sent back.", ledger.WorkReject, "sent back"
	}
	if note != "" {
		words += " " + note
	}
	sum := sha256.Sum256([]byte(words))
	ref := fmt.Sprintf("work-review:%s:%s:%d:%s", key, work.ID, work.InReviewSince().UnixMilli(), hex.EncodeToString(sum[:6]))
	utterance := ledger.Utterance{ProjectKey: key, Ref: ref, SpeakerID: surface.PersonSessionID, TargetID: work.Assignee, Text: words, At: time.Now(), CapturedBy: ledger.CapturedByAsk}
	if session, err := d.Registry.Session(work.Assignee); err == nil && session != nil {
		utterance.Surface = string(session.Surface)
	}
	if _, _, err := store.RecordUtterance(utterance); err != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: work.Assignee, Action: action, ItemID: work.ID, Reason: note, UtteranceID: ref}, time.Now()); err != nil {
		var refusal *ledger.Refusal
		if now, _ := board.WorkItem(work.ID); now.State != ledger.WorkInReview || !errors.As(err, &refusal) {
			return actionResult{}, staleRequest()
		}
		return actionResult{}, actionFailed(http.StatusConflict, "refused", err.Error())
	}
	message := fmt.Sprintf("[intent ledger] The person %s %s (%q).", outcome, work.ID, work.Text)
	if note != "" {
		message += " " + note
	}
	if _, err := d.sendNotice(noticeRecipient{sessionID: work.Assignee, role: "holder"}, message, "work-review:"+ref); err != nil {
		d.log.Printf("reviews: tell %s about %s: %s", d.resolveDisplay(work.Assignee), work.ID, err)
	}
	d.decisions.poke()
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"outcome": outcome, "workItem": work.ID, "utteranceId": ref}}), nil
}
