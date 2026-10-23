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
	briefUserBytes      = 600
	briefAssistantBytes = 1200
	briefMissingBytes   = 200
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
}

// Briefer reads a session's records for its voice brief. It makes no model
// call.
type Briefer interface {
	BriefSource(ctx context.Context, session *surface.Session) BriefSource
}

// BriefInfo is what a call was told about its session, as the phone shows it.
type BriefInfo struct {
	SessionID  string    `json:"sessionId"`
	AsOf       time.Time `json:"asOf"`
	JournalSeq uint64    `json:"journalSeq,omitempty"`
	Bytes      int       `json:"bytes"`
	Exchanges  int       `json:"exchanges"`
	Omitted    []string  `json:"omitted,omitempty"`
}

// RenderBrief writes the brief voice is given about its session, newest
// exchanges first, within briefBytes. Every part it leaves out or shortens is
// named in the brief and in the returned info.
func RenderBrief(src BriefSource) (string, BriefInfo) {
	info := BriefInfo{SessionID: src.Session.ID, AsOf: src.AsOf, JournalSeq: src.JournalSeq}
	var omitted []string
	for _, missing := range src.Missing {
		omitted = append(omitted, clip(missing, briefMissingBytes))
	}
	var head strings.Builder
	fmt.Fprintf(&head, "Context for the session %q (%s, id %s), read from its own records at %s. ", src.Session.Name, src.Session.Surface, src.Session.ID, src.AsOf.UTC().Format(time.RFC3339))
	head.WriteString("It is everything you know about the session. For anything not in it, say you don't know, and offer to ask the session.\n")
	switch src.Session.Status {
	case surface.StatusBusy:
		head.WriteString("Status: working on a turn now.\n")
	case surface.StatusIdle:
		head.WriteString("Status: idle.\n")
	default:
		fmt.Fprintf(&head, "Status: %s.\n", src.Session.Status)
	}
	if src.Goal != nil && strings.TrimSpace(src.Goal.Objective) != "" {
		fmt.Fprintf(&head, "Goal (%s): %s\n", src.Goal.Status, clip(src.Goal.Objective, briefUserBytes))
	}
	if src.Compactions != nil {
		fmt.Fprintf(&head, "Context compactions so far: %d. Details from before the latest one may no longer be in the session's memory.\n", *src.Compactions)
	}

	var shown []string
	budget := briefBytes - head.Len() - 512
	kept := 0
	for i := len(src.Exchanges) - 1; i >= 0; i-- {
		block, shortened := exchangeBlock(src.Exchanges[i])
		if len(block) > budget {
			break
		}
		budget -= len(block)
		shown = append(shown, block)
		kept++
		if shortened {
			omitted = append(omitted, "an exchange at "+src.Exchanges[i].Timestamp.UTC().Format(time.RFC3339)+" was shortened")
		}
	}
	if dropped := len(src.Exchanges) - kept; dropped > 0 {
		omitted = append(omitted, fmt.Sprintf("%d earlier exchange(s) left out for length", dropped))
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
	if len(omitted) > 0 {
		b.WriteString("Not included: " + strings.Join(omitted, "; ") + ".\n")
	}
	info.Bytes, info.Exchanges, info.Omitted = b.Len(), kept, omitted
	return b.String(), info
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
func buildBrief(ctx context.Context, briefer Briefer, session *surface.Session) (string, *BriefInfo) {
	if briefer == nil || session == nil {
		return "", nil
	}
	text, info := RenderBrief(briefer.BriefSource(ctx, session))
	return text, &info
}
