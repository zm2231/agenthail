package voice

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

// briefBytes bounds a session brief. Codex limits a realtime session's
// initial items to 8,192 estimated tokens in total, and the brief shares
// them with nothing else, so this leaves wide room.
const briefBytes = 6 << 10

const (
	briefNameBytes      = 200
	briefIDBytes        = 128
	briefStatusBytes    = 64
	briefGoalBytes      = 600
	briefUserBytes      = 600
	briefAssistantBytes = 1200
	briefMissingBytes   = 200
	// briefOmittedBytes is the room kept for the line naming what the brief
	// left out. Labels past it are counted rather than listed.
	briefOmittedBytes = 1024
)

// BriefSource is what the host read from a session's own records for a voice
// brief. Exchanges are oldest first. Missing names each part that could not be
// read, with why.
type BriefSource struct {
	Session     surface.Session
	Goal        *surface.GoalState
	Compactions *int
	Exchanges   []surface.Exchange
	JournalSeq  uint64
	Missing     []string
	AsOf        time.Time
	Earlier     []EarlierRequest
}

// Briefer reads a session's records for its voice brief. It makes no model
// call.
type Briefer interface {
	BriefSource(ctx context.Context, session *surface.Session) BriefSource
	// TurnOutcomes reads what the session's records say about each turn.
	TurnOutcomes(ctx context.Context, session *surface.Session, turnIDs []string) (map[string]TurnOutcome, error)
}

// BriefInfo is what a call was told about its session, as the phone shows it.
type BriefInfo struct {
	SessionID  string    `json:"sessionId"`
	AsOf       time.Time `json:"asOf"`
	JournalSeq uint64    `json:"journalSeq,omitempty"`
	Bytes      int       `json:"bytes"`
	Exchanges  int       `json:"exchanges"`
	Omitted    []string  `json:"omitted,omitempty"`
	// Compactions is the session's compaction count when the brief was read,
	// so a later compaction is noticed.
	Compactions *int `json:"compactions,omitempty"`
}

// RenderBrief writes the brief voice is given about its session, newest
// exchanges first, within briefBytes. Every part it leaves out or shortens is
// named in the brief and in the returned info.
func RenderBrief(src BriefSource) (string, BriefInfo) {
	info := BriefInfo{SessionID: src.Session.ID, AsOf: src.AsOf, JournalSeq: src.JournalSeq, Compactions: src.Compactions}
	var omitted []string
	field := func(text string, limit int, label string) string {
		text, short := clipped(text, limit)
		if short {
			omitted = append(omitted, label+" was shortened")
		}
		return text
	}
	var head strings.Builder
	fmt.Fprintf(&head, "Context for the session %q (%s, id %s), read from its own records at %s. ", field(src.Session.Name, briefNameBytes, "the session name"), field(string(src.Session.Surface), briefStatusBytes, "the session's provider"), field(src.Session.ID, briefIDBytes, "the session id"), src.AsOf.UTC().Format(time.RFC3339))
	head.WriteString("It is everything you know about the session. For anything not in it, say you don't know, and offer to ask the session.\n")
	switch src.Session.Status {
	case surface.StatusBusy:
		head.WriteString("Status: working on a turn now.\n")
	case surface.StatusIdle:
		head.WriteString("Status: idle.\n")
	default:
		fmt.Fprintf(&head, "Status: %s.\n", field(string(src.Session.Status), briefStatusBytes, "the status"))
	}
	if src.Goal != nil && strings.TrimSpace(src.Goal.Objective) != "" {
		fmt.Fprintf(&head, "Goal (%s): %s\n", field(src.Goal.Status, briefStatusBytes, "the goal status"), field(src.Goal.Objective, briefGoalBytes, "the goal"))
	}
	if src.Compactions != nil {
		fmt.Fprintf(&head, "Context compactions so far: %d. Details from before the latest one may no longer be in the session's memory.\n", *src.Compactions)
	}
	if left := writeEarlier(&head, src.Earlier); left > 0 {
		omitted = append(omitted, fmt.Sprintf("%d more earlier request(s) kept for a later call", left))
	}

	var shown []string
	budget := briefBytes - head.Len() - len("Recent exchanges, newest first:\n") - briefOmittedBytes
	shortened := 0
	for i := len(src.Exchanges) - 1; i >= 0; i-- {
		block, short := exchangeBlock(src.Exchanges[i])
		if len(block) > budget {
			break
		}
		budget -= len(block)
		shown = append(shown, block)
		if short {
			shortened++
		}
	}
	if dropped := len(src.Exchanges) - len(shown); dropped > 0 {
		omitted = append(omitted, fmt.Sprintf("%d earlier exchange(s) left out for length", dropped))
	}
	if shortened > 0 {
		omitted = append(omitted, fmt.Sprintf("%d of the exchanges shown were shortened", shortened))
	}
	for _, missing := range src.Missing {
		omitted = append(omitted, clip(missing, briefMissingBytes))
	}
	var b strings.Builder
	b.WriteString(head.String())
	if len(shown) == 0 {
		b.WriteString("Recent exchanges: none supplied.\n")
	} else {
		b.WriteString("Recent exchanges, newest first:\n")
		for _, block := range shown {
			b.WriteString(block)
		}
	}
	b.WriteString(omittedLine(omitted))
	info.Bytes, info.Exchanges, info.Omitted = b.Len(), len(shown), omitted
	return b.String(), info
}

// omittedLine names what the brief left out within briefOmittedBytes, and
// counts the labels that do not fit.
func omittedLine(omitted []string) string {
	if len(omitted) == 0 {
		return ""
	}
	const prefix, suffix = "Not included: ", ".\n"
	room := briefOmittedBytes - len(prefix) - len(suffix) - len(fmt.Sprintf("; and %d more", len(omitted)))
	listed := make([]string, 0, len(omitted))
	used := 0
	for _, label := range omitted {
		if used+len(label)+2 > room {
			break
		}
		used += len(label) + 2
		listed = append(listed, label)
	}
	line := strings.Join(listed, "; ")
	switch rest := len(omitted) - len(listed); {
	case rest > 0 && line == "":
		line = fmt.Sprintf("%d part(s)", rest)
	case rest > 0:
		line += fmt.Sprintf("; and %d more", rest)
	}
	return prefix + line + suffix
}

func exchangeBlock(e surface.Exchange) (string, bool) {
	user, userShort := clipped(e.User, briefUserBytes)
	reply, replyShort := clipped(e.Assistant, briefAssistantBytes)
	who := "Person"
	if e.Sender != "" {
		who = e.Sender
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- %s at %s: %s\n", who, e.Timestamp.UTC().Format(time.RFC3339), user)
	if strings.TrimSpace(reply) == "" {
		b.WriteString("  Session: (no reply recorded)\n")
	} else {
		fmt.Fprintf(&b, "  Session: %s\n", reply)
	}
	return b.String(), userShort || replyShort
}

func clip(text string, limit int) string {
	clipped, _ := clipped(text, limit)
	return clipped
}

func clipped(text string, limit int) (string, bool) {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + " …", true
}

// buildBrief reads and renders the brief for a session, or nothing when there
// is no session or no briefer.
func buildBrief(ctx context.Context, briefer Briefer, session *surface.Session, requests []requestRecord) (string, *BriefInfo, map[string]TurnOutcome) {
	if briefer == nil || session == nil {
		return "", nil, nil
	}
	src := briefer.BriefSource(ctx, session)
	outcomes := earlierOutcomes(ctx, briefer, session, requests, &src)
	text, info := RenderBrief(src)
	return text, &info, outcomes
}
