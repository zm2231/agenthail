package ledger

import (
	"fmt"
	"slices"
	"strings"
)

// Takeover is what a session other than the holder does with a claimed item.
// Holder and Verdict are the daemon's judgment of the holder when the request
// was made; replay never judges again.
type Takeover struct {
	Then    string `json:"then"`
	Holder  string `json:"holder,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Force   bool   `json:"force,omitempty"`
	PR      string `json:"pr,omitempty"`
}

const (
	TakeoverRelease = "release"
	TakeoverSubmit  = "submit"
	TakeoverDone    = "done"
)

const (
	HolderArchived = "archived"
	HolderGone     = "gone"
	HolderLive     = "live"
	HolderUnknown  = "unknown"
)

// WorkSourcePRWatcher marks the logs the daemon's PR watcher writes.
const WorkSourcePRWatcher = "pr-watcher"

const prMergedPrefix = "PR merged: "

func (b *Board) planWorkTakeover(req WorkRequest) (EventType, any, string, error) {
	item := b.workItems[req.ItemID]
	if item == nil {
		return "", nil, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	if item.State != WorkInProgress && item.State != WorkBlocked || item.Assignee == "" {
		return "", nil, "", refuse("%s is %s with no holder; takeover is for claimed work that is in_progress or blocked", item.ID, item.State)
	}
	t := req.Takeover
	if t == nil || !slices.Contains([]string{TakeoverRelease, TakeoverSubmit, TakeoverDone}, t.Then) {
		return "", nil, "", refuse("takeover needs --then release, submit or done")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		return "", nil, "", refuse("takeover needs --reason: why %s's holder will not finish it", item.ID)
	}
	if item.Assignee == req.SessionID {
		return "", nil, "", refuse("you hold %s; submit or hand it off yourself", item.ID)
	}
	if t.Holder != item.Assignee {
		return "", nil, "", refuse("%s is held by %s, not %q as judged; check it again before taking it over", item.ID, item.Assignee, t.Holder)
	}
	switch t.Verdict {
	case HolderArchived, HolderGone:
	case HolderLive, HolderUnknown:
		if req.UtteranceID == "" || !t.Force {
			whose := map[string]string{HolderLive: "is still running", HolderUnknown: "runs on a host that did not answer"}[t.Verdict]
			return "", nil, "", refuse("%s's holder %s %s; ask it to hand %s off, or the person takes it over with --utterance <their words> --force", item.ID, item.Assignee, whose, item.ID)
		}
	default:
		return "", nil, "", refuse("%s's holder was not judged; takeover goes through the Agenthail daemon", item.ID)
	}
	if t.Force && req.UtteranceID == "" {
		return "", nil, "", refuse("--force is the person's: pass --utterance <their words>")
	}
	if req.UtteranceID != "" {
		if _, err := b.personWords(req, item, b.reviews.stateSeq[item.ID], "was claimed", "takeover"); err != nil {
			return "", nil, "", err
		}
	}
	taken := Takeover{Then: t.Then, Holder: item.Assignee, Verdict: t.Verdict, Force: t.Force}
	payload := WorkItemStatePayload{ItemID: item.ID, FromState: item.State, Actor: req.SessionID, Reason: reason, UtteranceID: req.UtteranceID, Takeover: &taken}
	switch t.Then {
	case TakeoverRelease:
		payload.ToState = WorkOpen
	case TakeoverSubmit:
		if err := b.refuseOpenChildren(item, WorkSubmit); err != nil {
			return "", nil, "", err
		}
		payload.ToState, payload.Assignee = WorkInReview, req.SessionID
	case TakeoverDone:
		if err := b.refuseOpenChildren(item, WorkApprove); err != nil {
			return "", nil, "", err
		}
		taken.PR = mergedPR(item)
		if taken.PR == "" {
			return "", nil, "", refuse("%s has no %q line from the PR watcher; submit it for review instead", item.ID, strings.TrimSpace(prMergedPrefix))
		}
		payload.ToState, payload.Assignee = WorkDone, item.Assignee
	}
	readback := fmt.Sprintf("%s %s (%s): %s", item.ID, payload.ToState, item.origin(), takeoverText(taken))
	return EventWorkItemState, payload, readback, nil
}

// mergedPR is the newest PR the watcher logged as merged on the item.
func mergedPR(item *WorkItem) string {
	for i := len(item.Timeline) - 1; i >= 0; i-- {
		e := item.Timeline[i]
		if e.Kind == "log" && e.Source == WorkSourcePRWatcher && strings.HasPrefix(e.Text, prMergedPrefix) {
			return strings.TrimPrefix(e.Text, prMergedPrefix)
		}
	}
	return ""
}

func takeoverText(t Takeover) string {
	verdict := t.Verdict
	if t.Force {
		verdict += ", forced by the person"
	}
	text := fmt.Sprintf("taken over from %s (%s)", t.Holder, verdict)
	switch t.Then {
	case TakeoverRelease:
		text += ", released for the next session"
	case TakeoverSubmit:
		text += ", submitted for review"
	case TakeoverDone:
		text += ", done by " + prMergedPrefix + t.PR
	}
	return text
}
