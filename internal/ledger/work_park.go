package ledger

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type ParkKind string

const (
	ParkUsage  ParkKind = "usage"
	ParkIdea   ParkKind = "idea"
	ParkLater  ParkKind = "later"
	ParkPerson ParkKind = "person"
)

var parkKinds = []ParkKind{ParkUsage, ParkIdea, ParkLater, ParkPerson}

type Park struct {
	Kind    ParkKind         `json:"kind"`
	Why     string           `json:"why,omitempty"`
	Usage   *ParkUsageTarget `json:"usage,omitempty"`
	Idea    *ParkIdeaShape   `json:"idea,omitempty"`
	Person  *ParkQuestion    `json:"person,omitempty"`
	Revisit time.Time        `json:"revisit,omitzero"`
	Session string           `json:"session,omitempty"`
}

type ParkUsageTarget struct {
	Provider string    `json:"provider"`
	Root     string    `json:"root,omitempty"`
	HostID   string    `json:"hostId,omitempty"`
	ResetsAt time.Time `json:"resetsAt,omitzero"`
}

type ParkIdeaShape struct {
	UtteranceID string `json:"utteranceId"`
	Goal        string `json:"goal,omitempty"`
	Plan        string `json:"plan,omitempty"`
	FirstTest   string `json:"firstTest,omitempty"`
}

type ParkQuestion struct {
	Question string `json:"question"`
	AskID    string `json:"askId,omitempty"`
}

func (b *Board) planPark(req WorkRequest, item *WorkItem, payload *WorkItemStatePayload) (string, error) {
	if req.Park == nil {
		return "", refuse("park needs a kind: %s", joinParkKinds())
	}
	park := *req.Park
	park.Why = strings.TrimSpace(park.Why)
	if item.State == WorkInProgress && item.Assignee != req.SessionID && item.Assignee != req.ForSession {
		return "", refuse("%s is claimed by %s; only its assignee parks it, or name it with --for-session", item.ID, item.Assignee)
	}
	switch park.Kind {
	case ParkUsage:
		if park.Usage == nil || (park.Usage.Provider != "claude" && park.Usage.Provider != "codex") {
			return "", refuse("park --usage needs the provider it waits on: claude or codex, optionally with its root (claude:<root>)")
		}
	case ParkIdea:
		if park.Idea == nil || park.Idea.UtteranceID == "" {
			return "", refuse("an idea keeps the person's words; park --idea needs --utterance <ref>")
		}
		if b.utterances[park.Idea.UtteranceID] == nil {
			return "", refuse("%q is not a captured utterance of the person's in %s", park.Idea.UtteranceID, req.ProjectKey)
		}
	case ParkLater:
		if park.Why == "" {
			return "", refuse("park --later needs why %s waits", item.ID)
		}
	case ParkPerson:
		if park.Person == nil || strings.TrimSpace(park.Person.Question) == "" {
			return "", refuse("park --person needs the question the person has to decide")
		}
		park.Person.Question = strings.TrimSpace(park.Person.Question)
	default:
		return "", refuse("park kind %q is not one of %s", park.Kind, joinParkKinds())
	}
	park.Session = item.Assignee
	if park.Session == "" {
		park.Session = req.SessionID
	}
	payload.Park, payload.Assignee = &park, ""
	if !req.Handoff.empty() {
		handoff := req.Handoff
		handoff.By = park.Session
		payload.Handoff = &handoff
	}
	return fmt.Sprintf("%s parked (%s: %s) (%s)", item.ID, park.Kind, park.Summary(), item.origin()), nil
}

func (p Park) Summary() string {
	var parts []string
	switch p.Kind {
	case ParkUsage:
		target := p.Usage.Provider
		if p.Usage.Root != "" {
			target += ":" + p.Usage.Root
		}
		parts = append(parts, "waits for "+target+" headroom")
	case ParkIdea:
		parts = append(parts, "the person's idea, "+p.Idea.UtteranceID)
	case ParkPerson:
		parts = append(parts, "waits on the person: "+p.Person.Question)
	}
	if p.Why != "" {
		parts = append(parts, p.Why)
	}
	if !p.Revisit.IsZero() {
		parts = append(parts, "revisit "+p.Revisit.UTC().Format("2006-01-02"))
	}
	return strings.Join(parts, "; ")
}

func (p Park) clone() Park {
	out := p
	if p.Usage != nil {
		usage := *p.Usage
		out.Usage = &usage
	}
	if p.Idea != nil {
		idea := *p.Idea
		out.Idea = &idea
	}
	if p.Person != nil {
		person := *p.Person
		out.Person = &person
	}
	return out
}

func joinParkKinds() string {
	names := make([]string, len(parkKinds))
	for i, kind := range parkKinds {
		names[i] = string(kind)
	}
	return strings.Join(names, ", ")
}

func (b *Board) parkedOnly(c Clause) bool {
	parked := false
	for _, item := range c.WorkItems {
		switch {
		case item.State == WorkParked:
			parked = true
		case !item.State.settled():
			return false
		}
	}
	return parked
}

func ParseParkKind(text string) (ParkKind, bool) {
	kind := ParkKind(strings.TrimSpace(text))
	return kind, slices.Contains(parkKinds, kind)
}
