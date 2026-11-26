package ledger

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ProofAdd     = "add"
	ProofReplace = "replace"
	ProofRemove  = "remove"

	ProofPass    = "pass"
	ProofFail    = "fail"
	ProofPending = "pending"

	maxProofSteps       = 30
	maxProofTitle       = 120
	maxProofText        = 1000
	maxProofRevision    = 64
	maxProofAttachments = 5
	maxProofLinkTitle   = 80
	maxProofOperationID = 64
)

var proofOutcomes = []string{ProofPass, ProofFail, ProofPending}

type ProofLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type ProofStep struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Expected    string      `json:"expected"`
	Observed    string      `json:"observed,omitempty"`
	Outcome     string      `json:"outcome"`
	Revision    string      `json:"revision,omitempty"`
	Attachments []ProofLink `json:"attachments,omitempty"`
	Version     int         `json:"version"`
	By          string      `json:"by"`
	At          time.Time   `json:"at"`
	Stale       bool        `json:"stale,omitempty"`
	round       int
}

type ProofCounts struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Pending int `json:"pending"`
}

type ProofWrite struct {
	Op          string    `json:"op"`
	Step        ProofStep `json:"step"`
	Base        int       `json:"base,omitempty"`
	OperationID string    `json:"operationId"`
}

type WorkProofPayload struct {
	ItemID string    `json:"itemId"`
	Op     string    `json:"op"`
	Step   ProofStep `json:"step"`
	Actor  string    `json:"actor"`
}

func proofRef(itemID, operationID string) string {
	return "proof:" + itemID + ":" + operationID
}

func (s *Store) workProof(req WorkRequest, now time.Time) (WorkResult, error) {
	w := req.Proof
	if w == nil {
		return WorkResult{}, refuse("proof needs op (add, replace or remove), the step and an operationId")
	}
	operation := strings.TrimSpace(w.OperationID)
	if operation == "" || utf8.RuneCountInString(operation) > maxProofOperationID {
		return WorkResult{}, refuse("proof needs an operationId of at most %d characters, the same on every retry of one write", maxProofOperationID)
	}
	board, release, err := s.lockBoard(req.ProjectKey)
	if err != nil {
		return WorkResult{}, err
	}
	defer release()
	tx, err := s.begin(req.ProjectKey)
	if err != nil {
		return WorkResult{}, err
	}
	defer tx.Rollback()
	if err := plannedHere(tx, req.ProjectKey); err != nil {
		return WorkResult{}, err
	}
	req = board.canonicalWorkRefs(req)
	ref := proofRef(req.ItemID, operation)
	var seen int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND ref=?`, req.ProjectKey, ref).Scan(&seen); err != nil {
		return WorkResult{}, err
	}
	readback := fmt.Sprintf("proof on %s already recorded under operation %s", req.ItemID, operation)
	if seen == 0 {
		payload, text, err := board.planProof(req, *w)
		if err != nil {
			return WorkResult{}, err
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return WorkResult{}, err
		}
		if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: req.ProjectKey, Type: EventWorkProof, Payload: data, ActorSessionID: req.SessionID, Ref: ref, CreatedAt: now.UTC()}); err != nil {
			return WorkResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return WorkResult{}, err
		}
		readback = text
	} else if err := tx.Rollback(); err != nil {
		return WorkResult{}, err
	}
	if err := s.catchUp(board, req.ProjectKey); err != nil {
		return WorkResult{}, err
	}
	item, _ := board.WorkItem(req.ItemID)
	return WorkResult{Item: item, Readback: "intent ledger: " + readback}, nil
}

func (b *Board) planProof(req WorkRequest, w ProofWrite) (WorkProofPayload, string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	item := b.workItems[req.ItemID]
	if item == nil {
		return WorkProofPayload{}, "", refuse("%q is not a work item in %s", req.ItemID, req.ProjectKey)
	}
	if item.Assignee == "" || item.Assignee != req.SessionID || item.State.settled() || item.State == WorkParked {
		return WorkProofPayload{}, "", refuse("only the session holding %s writes its proof, while it is held", item.ID)
	}
	payload := WorkProofPayload{ItemID: item.ID, Op: w.Op, Actor: req.SessionID}
	current := func() (*ProofStep, error) {
		i := slices.IndexFunc(item.Proof, func(p ProofStep) bool { return p.ID == w.Step.ID })
		if i < 0 {
			return nil, refuse("%s has no proof step %q", item.ID, w.Step.ID)
		}
		if step := &item.Proof[i]; step.Version != w.Base {
			return nil, refuse("proof step %s of %s is at version %d, not %d; read it again and write over what is there now", step.ID, item.ID, step.Version, w.Base)
		}
		return &item.Proof[i], nil
	}
	switch w.Op {
	case ProofAdd:
		if len(item.Proof) >= maxProofSteps {
			return WorkProofPayload{}, "", refuse("%s already has %d proof steps, the most an item holds; replace or remove one", item.ID, maxProofSteps)
		}
		step, err := proofFields(w.Step)
		if err != nil {
			return WorkProofPayload{}, "", err
		}
		step.ID, step.Version = fmt.Sprintf("p%d", item.proofIssued+1), 1
		payload.Step = step
		return payload, fmt.Sprintf("proof step %s added to %s (%s)", step.ID, item.ID, step.Outcome), nil
	case ProofReplace:
		was, err := current()
		if err != nil {
			return WorkProofPayload{}, "", err
		}
		step, err := proofFields(w.Step)
		if err != nil {
			return WorkProofPayload{}, "", err
		}
		step.ID, step.Version = was.ID, was.Version+1
		payload.Step = step
		return payload, fmt.Sprintf("proof step %s of %s replaced (%s, version %d)", step.ID, item.ID, step.Outcome, step.Version), nil
	case ProofRemove:
		was, err := current()
		if err != nil {
			return WorkProofPayload{}, "", err
		}
		payload.Step = ProofStep{ID: was.ID}
		return payload, fmt.Sprintf("proof step %s removed from %s", was.ID, item.ID), nil
	}
	return WorkProofPayload{}, "", refuse("proof op %q is not add, replace or remove", w.Op)
}

func proofFields(in ProofStep) (ProofStep, error) {
	step := ProofStep{Title: oneLine(in.Title), Expected: strings.TrimSpace(in.Expected), Observed: strings.TrimSpace(in.Observed), Outcome: strings.TrimSpace(in.Outcome), Revision: strings.TrimSpace(in.Revision)}
	if step.Outcome == "" {
		step.Outcome = ProofPending
	}
	switch {
	case step.Title == "" || utf8.RuneCountInString(step.Title) > maxProofTitle:
		return ProofStep{}, refuse("a proof step needs a title of at most %d characters: what was checked", maxProofTitle)
	case step.Expected == "" || utf8.RuneCountInString(step.Expected) > maxProofText:
		return ProofStep{}, refuse("a proof step needs expected, at most %d characters: what should happen", maxProofText)
	case utf8.RuneCountInString(step.Observed) > maxProofText:
		return ProofStep{}, refuse("observed is at most %d characters", maxProofText)
	case !slices.Contains(proofOutcomes, step.Outcome):
		return ProofStep{}, refuse("outcome %q is not pass, fail or pending", step.Outcome)
	case step.Outcome != ProofPending && step.Observed == "":
		return ProofStep{}, refuse("a step that passed or failed says what was observed")
	case utf8.RuneCountInString(step.Revision) > maxProofRevision || strings.ContainsAny(step.Revision, " \t\r\n"):
		return ProofStep{}, refuse("revision is one word of at most %d characters: the commit or build it was observed at", maxProofRevision)
	case len(in.Attachments) > maxProofAttachments:
		return ProofStep{}, refuse("a proof step takes at most %d attachments", maxProofAttachments)
	}
	for _, link := range in.Attachments {
		title, target := oneLine(link.Title), strings.TrimSpace(link.URL)
		parsed, err := url.Parse(target)
		if title == "" || utf8.RuneCountInString(title) > maxProofLinkTitle || err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
			return ProofStep{}, refuse("an attachment is a title of at most %d characters and an http(s) link; files are linked, not stored", maxProofLinkTitle)
		}
		step.Attachments = append(step.Attachments, ProofLink{Title: title, URL: target})
	}
	return step, nil
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func (b *Board) applyWorkProof(event Event) error {
	var p WorkProofPayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	item := b.workItems[p.ItemID]
	if item == nil {
		return nil
	}
	step := p.Step
	step.By, step.At, step.round = p.Actor, event.CreatedAt, item.round
	i := slices.IndexFunc(item.Proof, func(s ProofStep) bool { return s.ID == step.ID })
	switch {
	case p.Op == ProofAdd && i < 0:
		item.Proof = append(item.Proof, step)
		item.proofIssued++
	case p.Op == ProofReplace && i >= 0:
		item.Proof[i] = step
	case p.Op == ProofRemove && i >= 0:
		item.Proof = slices.Delete(item.Proof, i, i+1)
	}
	item.refreshProof()
	return nil
}

func (item *WorkItem) refreshProof() {
	for i := range item.Proof {
		item.Proof[i].Stale = item.Proof[i].round < item.round
	}
}

func (item WorkItem) ProofCounts() ProofCounts {
	var counts ProofCounts
	for _, step := range item.Proof {
		switch {
		case step.Stale:
		case step.Outcome == ProofPass:
			counts.Pass++
		case step.Outcome == ProofFail:
			counts.Fail++
		default:
			counts.Pending++
		}
	}
	return counts
}
