package ledger

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

type Phase string

const (
	PhaseScope  Phase = "scope"
	PhaseBuild  Phase = "build"
	PhaseReview Phase = "review"
	PhaseVerify Phase = "verify"
	PhaseCheck  Phase = "check"
	PhaseShip   Phase = "ship"
)

var Phases = []Phase{PhaseScope, PhaseBuild, PhaseReview, PhaseVerify, PhaseCheck, PhaseShip}

var defaultPhaseLabels = map[Phase]string{
	PhaseScope: "Scope", PhaseBuild: "Build", PhaseReview: "Review", PhaseVerify: "Verify", PhaseCheck: "Check", PhaseShip: "Ship",
}

const (
	LogVerify = "verify"

	maxPhaseLabel = 24
)

type PRMark string

const (
	PROpened PRMark = "opened"
	PRMerged PRMark = "merged"
	PRClosed PRMark = "closed"
)

var prLinePrefixes = []struct {
	mark   PRMark
	prefix string
}{{PROpened, "PR opened: "}, {PRMerged, "PR merged: "}, {PRClosed, "PR closed unmerged: "}}

func PRLine(mark PRMark, url string) string {
	for _, p := range prLinePrefixes {
		if p.mark == mark {
			return p.prefix + url
		}
	}
	return ""
}

// ParsePRLine reads a PR watcher line. Only lines with source WorkSourcePRItem
// were written by the watcher; the caller checks that.
func ParsePRLine(text string) (PRMark, string, bool) {
	text = strings.TrimSpace(text)
	for _, p := range prLinePrefixes {
		if url, ok := strings.CutPrefix(text, p.prefix); ok && url != "" {
			return p.mark, url, true
		}
	}
	return "", "", false
}

func (item *WorkItem) refreshPhase() {
	item.Phase, item.PhaseSince = timelinePhase(item.Timeline)
}

// timelinePhase replays an item's own timeline. A Verify mark counts until a
// pull request is opened after it, the item changes holder, or it is sent
// back from review; being blocked and unblocked keeps it.
func timelinePhase(timeline []WorkEntry) (Phase, time.Time) {
	var (
		state                     WorkState
		holder                    string
		verified, merged, entered bool
		phase                     Phase
		since                     time.Time
	)
	open := map[string]bool{}
	for _, e := range timeline {
		switch {
		case e.State != "" && (e.Kind == "created" || e.Kind == "state" || e.Kind == "imported"):
			next := e.Assignee
			if e.Kind == "imported" || e.Kind == "created" {
				next = ""
			}
			if next != holder || state == WorkInReview && e.State == WorkInProgress {
				verified = false
			}
			state, holder, entered = e.State, next, true
		case e.Kind == "log" && e.Source == WorkSourcePRItem:
			mark, url, ok := ParsePRLine(e.Text)
			switch {
			case !ok:
			case mark == PROpened:
				open[url], verified = true, false
			case mark == PRMerged:
				delete(open, url)
				merged = true
			case mark == PRClosed:
				delete(open, url)
			}
		case e.Kind == "log" && e.LogType == LogVerify:
			verified = true
		}
		if !entered {
			continue
		}
		if next := phaseOf(state, holder != "", verified, merged, len(open) > 0); next != phase {
			phase, since = next, e.At
		}
	}
	return phase, since
}

func phaseOf(state WorkState, held, verified, merged, openPR bool) Phase {
	switch {
	case state == "" || state == WorkClosed || state == WorkDeleted:
		return ""
	case state == WorkDone || merged:
		return PhaseShip
	case state == WorkInReview:
		return PhaseCheck
	case !held || state == WorkParked:
		return PhaseScope
	case verified:
		return PhaseVerify
	case openPR:
		return PhaseReview
	}
	return PhaseBuild
}

type PhaseLabelsRequest struct {
	ProjectKey string            `json:"projectKey"`
	SessionID  string            `json:"sessionId"`
	Labels     map[string]string `json:"labels"`
}

type PhaseLabelsPayload struct {
	Labels map[Phase]string `json:"labels"`
	Actor  string           `json:"actor"`
}

func (s *Store) SetPhaseLabels(req PhaseLabelsRequest, now time.Time) (map[Phase]string, error) {
	if req.ProjectKey == "" || req.SessionID == "" {
		return nil, refuse("project and acting session are required")
	}
	if len(req.Labels) == 0 {
		return nil, refuse("name at least one phase: key=label, with keys %s", joinPhases())
	}
	labels := map[Phase]string{}
	for key, label := range req.Labels {
		phase := Phase(strings.TrimSpace(key))
		if _, known := defaultPhaseLabels[phase]; !known {
			return nil, refuse("%q is not a phase; the phases are %s", key, joinPhases())
		}
		label = strings.TrimSpace(label)
		if strings.ContainsAny(label, "\r\n") || utf8.RuneCountInString(label) > maxPhaseLabel {
			return nil, refuse("the label for %s must be one line of at most %d characters", phase, maxPhaseLabel)
		}
		labels[phase] = label
	}
	board, release, err := s.writePlanned(req.ProjectKey, req.SessionID, now, func(*Board, *idIssuer) (EventType, any, error) {
		return EventPhaseLabels, PhaseLabelsPayload{Labels: labels, Actor: req.SessionID}, nil
	})
	if err != nil {
		return nil, err
	}
	defer release()
	return board.PhaseLabels(), nil
}

func (b *Board) applyPhaseLabels(data []byte) error {
	var p PhaseLabelsPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	for phase, label := range p.Labels {
		if _, known := defaultPhaseLabels[phase]; !known {
			continue
		}
		if label == "" {
			delete(b.phaseLabels, phase)
			continue
		}
		b.phaseLabels[phase] = label
	}
	return nil
}

func (b *Board) PhaseLabels() map[Phase]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[Phase]string, len(defaultPhaseLabels))
	for phase, label := range defaultPhaseLabels {
		out[phase] = label
		if own := b.phaseLabels[phase]; own != "" {
			out[phase] = own
		}
	}
	return out
}

func joinPhases() string {
	names := make([]string, len(Phases))
	for i, p := range Phases {
		names[i] = string(p)
	}
	return strings.Join(names, ", ")
}
