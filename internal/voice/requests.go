package voice

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	requestsMax        = 16
	briefRequestsMax   = 6
	requestPromptBytes = 120
	requestReplyBytes  = 240
)

// requestRecord is kept until voice has its outcome; a request is never sent again.
type requestRecord struct {
	MessageID string `json:"messageId"`
	TargetID  string `json:"targetId"`
	TurnID    string `json:"turnId"`
	Prompt    string `json:"prompt"`
}

type TurnOutcome struct {
	Found  bool
	Done   bool
	Failed string
	Reply  string
}

type EarlierRequest struct {
	Prompt  string
	Outcome TurnOutcome
	Unread  bool
}

func writeEarlier(b *strings.Builder, earlier []EarlierRequest) (left int) {
	if len(earlier) == 0 {
		return 0
	}
	shown := earlier[:min(len(earlier), briefRequestsMax)]
	b.WriteString("Requests the person made by voice in an earlier call, not reported to them yet:\n")
	for _, request := range shown {
		fmt.Fprintf(b, "- %q: %s\n", clip(request.Prompt, requestPromptBytes), earlierOutcome(request))
	}
	return len(earlier) - len(shown)
}

func earlierOutcome(request EarlierRequest) string {
	outcome := request.Outcome
	switch {
	case request.Unread:
		return "its outcome could not be read."
	case !outcome.Found:
		return "its outcome is not in the session's recent records."
	case outcome.Failed != "":
		return "it ended without a reply: " + clip(outcome.Failed, briefMissingBytes) + "."
	case outcome.Done && strings.TrimSpace(outcome.Reply) != "":
		return "the session replied: " + firstSentences(outcome.Reply, requestReplyBytes)
	case outcome.Done:
		return "the session finished it without a written reply."
	default:
		return "the session is still working on it; its reply will follow."
	}
}

// The caller must not hold s.mu.
func (s *Service) briefFor(ctx context.Context, briefer Briefer, session *surface.Session) (string, *BriefInfo, map[string]TurnOutcome) {
	var requests []requestRecord
	if session != nil {
		s.mu.Lock()
		requests = s.requestsTo(session.ID)
		s.unlock()
	}
	return buildBrief(ctx, briefer, session, requests)
}

// The caller holds s.mu.
func (s *Service) keepRequest(r requestRecord) {
	r.Prompt = clip(r.Prompt, requestPromptBytes)
	s.state.Requests = append(s.state.Requests, r)
	if len(s.state.Requests) > requestsMax {
		dropped := s.state.Requests[0]
		s.appendEvent(Event{Method: "voice/request/dropped", Params: map[string]any{"messageId": dropped.MessageID, "targetId": dropped.TargetID, "turnId": dropped.TurnID}})
		s.state.Requests = s.state.Requests[1:]
	}
}

// The caller holds s.mu.
func (s *Service) requestsTo(targetID string) []requestRecord {
	var requests []requestRecord
	for _, r := range s.state.Requests {
		if targetID != "" && r.TargetID == targetID {
			requests = append(requests, r)
		}
	}
	return requests
}

// The caller holds s.mu.
func (s *Service) requestDone(turnID string) {
	kept := s.state.Requests[:0]
	for _, r := range s.state.Requests {
		if r.TurnID != turnID {
			kept = append(kept, r)
		}
	}
	s.state.Requests = kept
}

// reported drops what the brief told and returns the requests to follow. The caller holds s.mu.
func (s *Service) reported(target *surface.Session, outcomes map[string]TurnOutcome) []requestRecord {
	var open []requestRecord
	kept := s.state.Requests[:0]
	for _, r := range s.state.Requests {
		if target == nil || r.TargetID != target.ID {
			continue
		}
		outcome, told := outcomes[r.TurnID]
		if told && (!outcome.Found || outcome.Done) {
			continue
		}
		kept = append(kept, r)
		if told || outcomes == nil {
			open = append(open, r)
		}
	}
	s.state.Requests = kept
	return open
}

func earlierOutcomes(ctx context.Context, briefer Briefer, session *surface.Session, requests []requestRecord, src *BriefSource) map[string]TurnOutcome {
	if len(requests) == 0 {
		return nil
	}
	turns := make([]string, 0, briefRequestsMax)
	for _, r := range requests[:min(len(requests), briefRequestsMax)] {
		turns = append(turns, r.TurnID)
	}
	outcomes, err := briefer.TurnOutcomes(ctx, session, turns)
	if err != nil {
		src.Missing = append(src.Missing, "earlier requests' outcomes: "+err.Error())
		outcomes = nil
	}
	for _, r := range requests {
		src.Earlier = append(src.Earlier, EarlierRequest{Prompt: r.Prompt, Outcome: outcomes[r.TurnID], Unread: outcomes == nil})
	}
	return outcomes
}
