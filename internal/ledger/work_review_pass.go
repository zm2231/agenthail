package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ReviewPass struct {
	By     string    `json:"by"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type WorkReviewPassPayload struct {
	ItemID string `json:"itemId"`
	Reason string `json:"reason"`
	Actor  string `json:"actor"`
}

// The ledger checks only the item's state; who may pass a review on is the project's lead, which the daemon checks before it writes.
func (b *Board) planReviewPass(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	if item.State != WorkInReview {
		return "", nil, "", refuse("%s is %s; review-pass needs it %s", item.ID, item.State, WorkInReview)
	}
	if item.ReviewPass != nil {
		return "", nil, "", refuse("%s was already passed on to the person by %s", item.ID, item.ReviewPass.By)
	}
	reason := strings.TrimSpace(req.Reason)
	switch {
	case reason == "":
		return "", nil, "", refuse("review-pass needs --reason: what the person should decide about %s", item.ID)
	case utf8.RuneCountInString(reason) > maxWorkNoteRunes:
		return "", nil, "", refuse("a review-pass reason is at most %d characters", maxWorkNoteRunes)
	}
	return EventWorkReviewPass, WorkReviewPassPayload{ItemID: item.ID, Reason: reason, Actor: req.SessionID}, fmt.Sprintf("%s passed on to the person for review", item.ID), nil
}

func (b *Board) applyWorkReviewPass(event Event) error {
	var p WorkReviewPassPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil || item.State != WorkInReview || item.ReviewPass != nil {
		return nil
	}
	item.ReviewPass = &ReviewPass{By: p.Actor, Reason: p.Reason, At: event.CreatedAt}
	item.Timeline = append(item.Timeline, WorkEntry{At: event.CreatedAt, Actor: p.Actor, Kind: "review-pass", Text: p.Reason})
	return nil
}

func (item WorkItem) InReviewSince() time.Time {
	if item.State != WorkInReview {
		return time.Time{}
	}
	for i := len(item.Timeline) - 1; i >= 0; i-- {
		if e := item.Timeline[i]; e.State == WorkInReview && (e.Kind == "state" || e.Kind == "imported") {
			return e.At
		}
	}
	return time.Time{}
}
