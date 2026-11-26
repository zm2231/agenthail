package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const itemQuestionPath = "/api/v1/ledger/item-question"

type itemQuestionWrite struct {
	ProjectKey string                      `json:"projectKey"`
	SessionID  string                      `json:"sessionId"`
	Raise      *ledger.ItemQuestionRaise   `json:"raise,omitempty"`
	Resolve    *ledger.ItemQuestionResolve `json:"resolve,omitempty"`
}

type itemQuestionReply struct {
	QuestionID string `json:"questionId,omitempty"`
}

// Only a paired host may write an item question to this host's ledger: an agent's ask reaches it only through the daemon that holds the ask.
func (d *Daemon) apiLedgerItemQuestionHandler(w http.ResponseWriter, r *http.Request) {
	if _, isHost := requestingHost(r.Context()); !isHost {
		writeAPIError(w, http.StatusForbidden, "forbidden", "Only a paired Agenthail host writes an item question.")
		return
	}
	var req itemQuestionWrite
	if !decodeLedgerWrite(w, r, &req) {
		return
	}
	if owner, _, err := d.ledgerStore().Owner(req.ProjectKey); err != nil || owner != d.hostIdentity().ID {
		writeAPIError(w, http.StatusConflict, "refused", "This host does not own the ledger for "+req.ProjectKey+".")
		return
	}
	reply, err := d.writeItemQuestionHere(req)
	if errors.Is(err, ledger.ErrItemGone) {
		writeAPIError(w, http.StatusGone, "item_gone", err.Error())
		return
	}
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, reply)
}

func (d *Daemon) writeItemQuestionHere(req itemQuestionWrite) (itemQuestionReply, error) {
	store := d.ledgerStore()
	switch {
	case req.Raise != nil && req.Resolve == nil:
		req.Raise.ProjectKey, req.Raise.SessionID = req.ProjectKey, req.SessionID
		id, err := store.RaiseItemQuestion(*req.Raise, time.Now())
		return itemQuestionReply{QuestionID: id}, err
	case req.Resolve != nil && req.Raise == nil:
		req.Resolve.ProjectKey, req.Resolve.SessionID = req.ProjectKey, req.SessionID
		return itemQuestionReply{}, store.ResolveItemQuestion(*req.Resolve, time.Now())
	}
	return itemQuestionReply{}, errors.New("an item question write is a raise or a resolve")
}

func (d *Daemon) writeItemQuestion(ctx context.Context, req itemQuestionWrite) (itemQuestionReply, error) {
	owner, err := d.ledgerStore().RemoteOwner(req.ProjectKey)
	if err != nil {
		return itemQuestionReply{}, err
	}
	if owner == "" {
		return d.writeItemQuestionHere(req)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return itemQuestionReply{}, err
	}
	answer, failure := d.writeAtLedgerOwner(ctx, owner, req.ProjectKey, itemQuestionPath, body)
	if failure != nil {
		return itemQuestionReply{}, failure
	}
	if answer.status == http.StatusGone {
		return itemQuestionReply{}, fmt.Errorf("%w: %s", ledger.ErrItemGone, answer.body)
	}
	if answer.status != http.StatusOK {
		return itemQuestionReply{}, fmt.Errorf("the ledger's owner answered %d: %s", answer.status, answer.body)
	}
	var reply itemQuestionReply
	return reply, json.Unmarshal(answer.body, &reply)
}

func (d *Daemon) syncItemQuestionsSoon() {
	if d.background != nil {
		d.background.start(d.syncItemQuestions)
	}
}

// Both writes carry a ref made from the ask, so retrying one whose outcome was lost records it once.
func (d *Daemon) syncItemQuestions(ctx context.Context) {
	d.itemQuestionsMu.Lock()
	defer d.itemQuestionsMu.Unlock()
	pending, err := d.Registry.PendingItemQuestions()
	if err != nil {
		d.log.Printf("item questions: read asks: %s", err)
		return
	}
	host := d.hostIdentity().ID
	for _, ask := range pending {
		if ask.LedgerQuestion == "" {
			raise := &ledger.ItemQuestionRaise{ItemID: ask.WorkItem, AskHostID: host, AskID: ask.ID, Title: ask.Title, Prompts: itemQuestionPrompts(ask)}
			if index, defaulted := askDefault(ask); defaulted {
				raise.Default, raise.DeadlineMs = &index, ask.Deadline.UnixMilli()
			}
			reply, err := d.writeItemQuestion(ctx, itemQuestionWrite{ProjectKey: ask.LedgerKey, SessionID: ask.SessionID, Raise: raise})
			if errors.Is(err, ledger.ErrItemGone) {
				d.log.Printf("item questions: %s is on %s, which is gone; it stays a plain ask", ask.ID, ask.WorkItem)
				if err := d.Registry.AbandonItemQuestion(ask.ID, time.Now()); err != nil {
					d.log.Printf("item questions: stop recording %s: %s", ask.ID, err)
				}
				continue
			}
			if err != nil || reply.QuestionID == "" {
				d.log.Printf("item questions: record %s on %s: %v", ask.ID, ask.WorkItem, err)
				continue
			}
			if err := d.Registry.RecordItemQuestion(ask.ID, reply.QuestionID); err != nil {
				d.log.Printf("item questions: keep %s on %s: %s", reply.QuestionID, ask.ID, err)
				continue
			}
			ask.LedgerQuestion = reply.QuestionID
		}
		outcome := itemQuestionOutcome(ask)
		if outcome == "" {
			continue
		}
		by := ask.AnsweredBy
		if by == "" {
			by = ask.SessionID
		}
		resolve := &ledger.ItemQuestionResolve{AskHostID: host, AskID: ask.ID, Answer: askAnswerLine(ask), By: by, Outcome: outcome}
		if _, err := d.writeItemQuestion(ctx, itemQuestionWrite{ProjectKey: ask.LedgerKey, SessionID: ask.SessionID, Resolve: resolve}); err != nil {
			d.log.Printf("item questions: resolve %s on %s: %s", ask.LedgerQuestion, ask.WorkItem, err)
			continue
		}
		if err := d.Registry.RecordItemQuestionResolved(ask.ID, time.Now()); err != nil {
			d.log.Printf("item questions: mark %s resolved: %s", ask.ID, err)
		}
	}
}

func itemQuestionOutcome(ask registry.Ask) string {
	if _, defaulted := askDefault(ask); defaulted && ask.State == registry.AskExpired {
		return ledger.ItemQuestionDefaulted
	}
	switch ask.State {
	case registry.AskAnswered:
		return ledger.ItemQuestionAnswered
	case registry.AskWithdrawn:
		return ledger.ItemQuestionWithdrawn
	case registry.AskExpired:
		return ledger.ItemQuestionExpired
	}
	return ""
}

func itemQuestionPrompts(ask registry.Ask) []ledger.ItemQuestionPrompt {
	prompts := make([]ledger.ItemQuestionPrompt, 0, len(ask.Questions))
	for _, question := range ask.Questions {
		prompt := ledger.ItemQuestionPrompt{Text: question.Question}
		for i, option := range question.Options {
			prompt.Options = append(prompt.Options, ledger.ItemQuestionOption{Label: option.Label, Does: option.Does})
			if option.ID == question.Recommended {
				recommended := i
				prompt.Recommended = &recommended
			}
		}
		for _, link := range question.Links {
			prompt.Links = append(prompt.Links, ledger.ItemQuestionLink{Title: link.Title, URL: link.URL})
		}
		prompts = append(prompts, prompt)
	}
	return prompts
}

func askAnswerLine(ask registry.Ask) string {
	if _, defaulted := askDefault(ask); defaulted && ask.State == registry.AskExpired {
		return askDefaultMessage(ask)
	}
	if ask.State != registry.AskAnswered {
		return ""
	}
	return composeAskAnswer(askRequest(ask).Questions, surface.Decision{Answers: ask.Answer.Answers, Message: ask.Answer.Note})
}
