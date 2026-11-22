package ledger

import "encoding/json"

type reviewState struct {
	utteranceSeq map[string]int64
	inReviewSeq  map[string]int64
	stateSeq     map[string]int64
	approvedBy   map[string]string
}

func newReviewState() reviewState {
	return reviewState{utteranceSeq: map[string]int64{}, inReviewSeq: map[string]int64{}, stateSeq: map[string]int64{}, approvedBy: map[string]string{}}
}

func (r reviewState) observe(event Event) error {
	switch event.Type {
	case EventUtterance:
		var p UtterancePayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		r.utteranceSeq[p.UtteranceID] = event.Seq
	case EventWorkItemState:
		var p WorkItemStatePayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		if p.ToState == WorkInReview {
			r.inReviewSeq[p.ItemID] = event.Seq
		}
		r.stateSeq[p.ItemID] = event.Seq
		if p.UtteranceID != "" {
			r.approvedBy[p.UtteranceID] = p.ItemID
		}
	}
	return nil
}

func (b *Board) reviewer(req WorkRequest, item *WorkItem) (string, error) {
	if req.UtteranceID == "" {
		if req.SessionID == item.Assignee {
			return "", refuse("%s was done by %s; another session reviews it, or the person with --utterance <their words>", item.ID, item.Assignee)
		}
		return req.SessionID, nil
	}
	return b.personWords(req, item, b.reviews.inReviewSeq[item.ID], "went into review", "review")
}

// personWords is the person who said the utterance a session records for an
// item: words captured from the person, said to that session after since,
// and not yet used for another item.
func (b *Board) personWords(req WorkRequest, item *WorkItem, since int64, after, what string) (string, error) {
	u := b.utterances[req.UtteranceID]
	if u == nil || !personCapture(u.CapturedBy) {
		return "", refuse("%s is not a captured person utterance in %s", req.UtteranceID, req.ProjectKey)
	}
	if u.TargetID != req.SessionID {
		return "", refuse("%s was said to %s, not to %s; the session the person spoke to records their %s", u.ID, u.TargetID, req.SessionID, what)
	}
	if b.reviews.utteranceSeq[u.ID] < since {
		return "", refuse("%s was said before %s %s; the person's %s is words said after it", u.ID, item.ID, after, what)
	}
	if used, ok := b.reviews.approvedBy[u.ID]; ok {
		return "", refuse("%s already recorded the person's %s of %s; one utterance decides one item", u.ID, what, used)
	}
	return u.SpeakerID, nil
}
